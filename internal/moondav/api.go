package moondav

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

type dashboardBook struct {
	Key            string    `json:"key"`
	BackendID      string    `json:"backend_id,omitempty"`
	EPUBPath       string    `json:"epub_path,omitempty"`
	MoonPercent    float64   `json:"moon_percent"`
	BackendPercent float64   `json:"backend_percent"`
	UpdatedAt      time.Time `json:"updated_at"`
	BackendUpdated time.Time `json:"backend_updated_at"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
}

func (a *App) apiDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	states := a.state.Snapshot()
	maps := a.bookMap.Snapshot()
	books := make([]dashboardBook, 0, len(states))
	var conflicts, unmapped, errors, queued int
	for key, e := range states {
		mapping, mapped := maps[key]
		status := "synced"
		switch {
		case e.LastError != "":
			status = "error"
			errors++
		case e.PendingSync:
			status = "queued"
			queued++
		case !mapped && a.cfg.BackendType != "none":
			status = "unmapped"
			unmapped++
		case e.RemoteAhead:
			status = "conflict"
			conflicts++
		case e.Percent > e.BackendPercent+0.01 && a.cfg.BackendType != "none":
			status = "moon-ahead"
		case e.BackendPercent > e.Percent+0.01:
			status = "remote-ahead"
		}
		books = append(books, dashboardBook{
			Key: key, BackendID: mapping.BackendID, EPUBPath: mapping.EPUBPath, MoonPercent: e.Percent, BackendPercent: e.BackendPercent,
			UpdatedAt: e.UpdatedAt, BackendUpdated: e.BackendUpdatedAt, Status: status, Error: e.LastError,
		})
	}
	sort.Slice(books, func(i, j int) bool { return books[i].UpdatedAt.After(books[j].UpdatedAt) })
	writeJSON(w, map[string]any{
		"backend": a.cfg.BackendType,
		"books": books,
		"summary": map[string]int{"books": len(books), "conflicts": conflicts, "unmapped": unmapped, "errors": errors, "queued": queued},
		"backend_health": a.state.Health(),
	})
}

func (a *App) apiMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, a.bookMap.Snapshot())
		return
	}
	if !sameOriginWrite(r) {
		http.Error(w, "cross-origin write blocked", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var v struct {
			BookKey   string `json:"book_key"`
			BackendID string `json:"backend_id"`
			EPUBPath  string `json:"epub_path"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&v); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := a.bookMap.SetDetailed(v.BookKey, BookMapping{BackendID: v.BackendID, EPUBPath: v.EPUBPath}); err != nil {
			http.Error(w, "invalid mapping", http.StatusBadRequest)
			return
		}
		go a.reconcile()
		writeJSON(w, map[string]bool{"ok": true})
	case http.MethodDelete:
		key := strings.TrimSpace(r.URL.Query().Get("book_key"))
		if key == "" {
			http.Error(w, "missing book_key", http.StatusBadRequest)
			return
		}
		if err := a.bookMap.Delete(key); err != nil {
			http.Error(w, "could not delete mapping", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) apiConflicts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOriginWrite(r) {
		http.Error(w, "cross-origin write blocked", http.StatusForbidden)
		return
	}
	var v struct {
		BookKey string `json:"book_key"`
		Action  string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&v); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	entry, ok := a.state.Get(strings.ToLower(strings.TrimSpace(v.BookKey)))
	if !ok {
		http.Error(w, "book not found", http.StatusNotFound)
		return
	}
	switch v.Action {
	case "keep-moon":
		entry.SuppressRemoteUntilPct = 0
		entry.RemoteAhead = false
		if err := a.state.Put(strings.ToLower(v.BookKey), entry); err != nil {
			http.Error(w, "state write failed", http.StatusInternalServerError)
			return
		}
		entry.PendingSync = true
		entry.PendingPercent = entry.Percent
		entry.NextRetryAt = time.Now().UTC()
		_ = a.state.Put(strings.ToLower(v.BookKey), entry)
		go a.push(strings.ToLower(v.BookKey))
	case "ignore-until-caught-up":
		entry.SuppressRemoteUntilPct = entry.BackendPercent
		entry.RemoteAhead = false
		if err := a.state.Put(strings.ToLower(v.BookKey), entry); err != nil {
			http.Error(w, "state write failed", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func sameOriginWrite(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Host
	return strings.HasSuffix(origin, "://"+host)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
