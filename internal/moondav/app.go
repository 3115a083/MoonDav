package moondav

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

type App struct {
	cfg     Config
	state   *StateStore
	backend Backend
	bookMap *BookMap
	dav     *webdav.Handler
}

func New(cfg Config) (*App, error) {
	st, err := OpenState(filepath.Join(cfg.DataDir, "state.json"))
	if err != nil {
		return nil, err
	}
	be, err := NewBackend(cfg)
	if err != nil {
		return nil, err
	}
	bm, err := OpenBookMap(cfg.BookMapFile)
	if err != nil {
		return nil, err
	}
	d := &webdav.Handler{
		Prefix:     cfg.BasePath,
		FileSystem: webdav.Dir(filepath.Join(cfg.DataDir, "webdav")),
		LockSystem: webdav.NewMemLS(),
	}
	a := &App{cfg: cfg, state: st, backend: be, bookMap: bm, dav: d}
	if cfg.BackendType != "none" {
		go a.reconcileLoop()
	}
	return a, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/", a.auth(a.uiIndex))
	mux.Handle("/assets/", a.authHandler(a.uiAssets()))
	mux.HandleFunc("/api/dashboard", a.auth(a.apiDashboard))
	mux.HandleFunc("/api/mappings", a.auth(a.apiMappings))
	mux.HandleFunc("/api/conflicts", a.auth(a.apiConflicts))
	mux.HandleFunc("/status", a.auth(a.status))
	mux.Handle(a.cfg.BasePath, a.auth(a.davHandler()))
	return secureHeaders(mux)
}

func (a *App) authHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(a.auth(next.ServeHTTP))
}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(a.cfg.DAVUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(a.cfg.DAVPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="MoonDav"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (a *App) davHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && IsPositionPath(r.URL.Path) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes))
			if err != nil {
				http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
				return
			}
			pos := ParseMoonPosition(body)
			key := BookKeyFromPOPath(r.URL.Path)

			if old, ok := a.state.Get(key); ok &&
				pos.Valid &&
				a.cfg.ConflictPolicy == "furthest" &&
				pos.Percent+0.01 < old.Percent {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			r.Body = io.NopCloser(strings.NewReader(string(body)))
			a.dav.ServeHTTP(w, r)

			if pos.Valid {
				entry := StateEntry{
					Path:      r.URL.Path,
					Percent:   pos.Percent,
					UpdatedAt: time.Now().UTC(),
				}
				if old, ok := a.state.Get(key); ok {
					entry.BackendPercent = old.BackendPercent
					entry.BackendUpdatedAt = old.BackendUpdatedAt
					entry.SuppressRemoteUntilPct = old.SuppressRemoteUntilPct
				}
				_ = a.state.Put(key, entry)
				go a.push(key, pos.Percent)
			}
			return
		}
		a.dav.ServeHTTP(w, r)
	}
}

func (a *App) push(bookKey string, p float64) {
	bookID, ok := a.bookMap.Resolve(bookKey)
	if !ok {
		if a.cfg.BackendType != "none" {
			e, _ := a.state.Get(bookKey)
			e.LastError = "No backend mapping"
			_ = a.state.Put(bookKey, e)
		}
		return
	}
	if err := a.backend.Push(bookID, p); err != nil {
		log.Printf("backend push %s: %v", bookID, err)
		e, _ := a.state.Get(bookKey)
		e.LastError = err.Error()
		_ = a.state.Put(bookKey, e)
		return
	}
	e, _ := a.state.Get(bookKey)
	e.BackendPercent = p
	e.BackendUpdatedAt = time.Now().UTC()
	e.RemoteAhead = false
	e.LastError = ""
	if p >= e.SuppressRemoteUntilPct {
		e.SuppressRemoteUntilPct = 0
	}
	_ = a.state.Put(bookKey, e)
}

func (a *App) reconcileLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.reconcile()
	for range ticker.C {
		a.reconcile()
	}
}

func (a *App) reconcile() {
	for key, entry := range a.state.Snapshot() {
		id, ok := a.bookMap.Resolve(key)
		if !ok {
			continue
		}

		if entry.Percent > entry.BackendPercent+0.01 {
			if err := a.backend.Push(id, entry.Percent); err != nil {
				entry.LastError = err.Error()
				_ = a.state.Put(key, entry)
				continue
			}
			entry.BackendPercent = entry.Percent
			entry.BackendUpdatedAt = time.Now().UTC()
			entry.RemoteAhead = false
			entry.LastError = ""
			_ = a.state.Put(key, entry)
		}

		remote, updated, err := a.backend.Pull(id)
		if err != nil {
			entry.LastError = err.Error()
			_ = a.state.Put(key, entry)
			continue
		}
		entry.LastError = ""
		if remote > entry.Percent+0.01 {
			entry.BackendPercent = remote
			entry.BackendUpdatedAt = updated
			entry.RemoteAhead = entry.SuppressRemoteUntilPct+0.01 < remote
			_ = a.state.Put(key, entry)
			if entry.RemoteAhead {
				log.Printf("remote progress %.2f%% is ahead of Moon+ %.2f%% for %s", remote, entry.Percent, key)
			}
		} else {
			entry.RemoteAhead = false
			_ = a.state.Put(key, entry)
		}
	}
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"backend":   a.cfg.BackendType,
		"base_path": a.cfg.BasePath,
		"entries":   a.state.Snapshot(),
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
