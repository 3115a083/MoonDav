package moondav

import (
	"crypto/subtle"
	"encoding/json"
	"io"
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
	mux.HandleFunc("/", a.adminAuth(a.uiIndex))
	mux.Handle("/assets/", a.adminAuthHandler(a.uiAssets()))
	mux.HandleFunc("/api/dashboard", a.adminAuth(a.apiDashboard))
	mux.HandleFunc("/api/mappings", a.adminAuth(a.apiMappings))
	mux.HandleFunc("/api/conflicts", a.adminAuth(a.apiConflicts))
	mux.HandleFunc("/status", a.adminAuth(a.status))
	mux.Handle(a.cfg.BasePath, a.davAuth(a.davHandler()))
	return secureHeaders(mux)
}

func (a *App) adminAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(a.basicAuth(next.ServeHTTP, a.cfg.AdminUser, a.cfg.AdminPassword, "MoonDav Admin"))
}

func (a *App) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return a.basicAuth(next, a.cfg.AdminUser, a.cfg.AdminPassword, "MoonDav Admin")
}

func (a *App) davAuth(next http.HandlerFunc) http.HandlerFunc {
	return a.basicAuth(next, a.cfg.DAVUser, a.cfg.DAVPassword, "MoonDav WebDAV")
}

func (a *App) basicAuth(next http.HandlerFunc, user, pass, realm string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
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
					entry.LastError = old.LastError
				}
				entry.PendingSync = a.cfg.BackendType != "none"
				entry.PendingPercent = pos.Percent
				entry.NextRetryAt = time.Now().UTC()
				_ = a.state.Put(key, entry)
				go a.push(key)
			}
			return
		}
		a.dav.ServeHTTP(w, r)
	}
}

func (a *App) push(bookKey string) {
	entry, ok := a.state.Get(bookKey)
	if !ok || a.cfg.BackendType == "none" {
		return
	}
	if !entry.NextRetryAt.IsZero() && time.Now().UTC().Before(entry.NextRetryAt) {
		return
	}
	bookID, ok := a.bookMap.Resolve(bookKey)
	if !ok {
		entry.PendingSync = true
		entry.PendingPercent = entry.Percent
		entry.LastError = "No backend mapping"
		entry.NextRetryAt = time.Now().UTC().Add(15 * time.Minute)
		_ = a.state.Put(bookKey, entry)
		return
	}

	target := entry.PendingPercent
	if !entry.PendingSync {
		target = entry.Percent
	}
	entry.LastSyncAttempt = time.Now().UTC()
	_ = a.state.Put(bookKey, entry)

	if err := a.backend.Push(bookID, target); err != nil {
		a.syncFailure(bookKey, err)
		return
	}
	a.backendSuccess()

	latest, _ := a.state.Get(bookKey)
	latest.BackendPercent = target
	latest.BackendUpdatedAt = time.Now().UTC()
	latest.RetryCount = 0
	latest.NextRetryAt = time.Time{}
	latest.LastError = ""
	latest.RemoteAhead = false
	if latest.PendingPercent <= target+0.01 {
		latest.PendingSync = false
		latest.PendingPercent = 0
	}
	if target >= latest.SuppressRemoteUntilPct {
		latest.SuppressRemoteUntilPct = 0
	}
	_ = a.state.Put(bookKey, latest)
}

func (a *App) syncFailure(bookKey string, err error) {
	entry, ok := a.state.Get(bookKey)
	if !ok {
		return
	}
	entry.PendingSync = true
	if entry.PendingPercent == 0 {
		entry.PendingPercent = entry.Percent
	}
	entry.RetryCount++
	entry.LastSyncAttempt = time.Now().UTC()

	if temporary(err) {
		entry.LastError = ""
		entry.NextRetryAt = time.Now().UTC().Add(retryDelay(entry.RetryCount))
		a.backendFailure("offline", err.Error())
	} else {
		entry.LastError = err.Error()
		entry.NextRetryAt = time.Now().UTC().Add(15 * time.Minute)
		a.backendFailure("error", err.Error())
	}
	_ = a.state.Put(bookKey, entry)
}

func retryDelay(attempt int) time.Duration {
	delays := []time.Duration{
		15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
		5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		return time.Hour
	}
	return delays[attempt-1]
}

func (a *App) backendFailure(state, message string) {
	now := time.Now().UTC()
	h := a.state.Health()
	if h.State == "" || h.State == "online" {
		h.DownSince = now
		h.LastNotification = time.Time{}
	}
	h.State = state
	h.LastFailure = now
	h.LastMessage = message
	_ = a.state.PutHealth(h)
	a.maybeNotifyOutage(h)
}

func (a *App) maybeNotifyOutage(h BackendHealth) {
	if h.DownSince.IsZero() || time.Since(h.DownSince) < a.cfg.NotifyAfter {
		return
	}
	if !h.LastNotification.IsZero() && time.Since(h.LastNotification) < a.cfg.NotifyRepeat {
		return
	}
	h.LastNotification = time.Now().UTC()
	_ = a.state.PutHealth(h)
	title := "MoonDav backend unavailable"
	if h.State == "error" {
		title = "MoonDav backend error"
	}
	go a.notify("backend_"+h.State, title, h.LastMessage)
}

func (a *App) backendSuccess() {
	now := time.Now().UTC()
	h := a.state.Health()
	wasDown := h.State == "offline" || h.State == "error"
	wasNotified := !h.LastNotification.IsZero()
	downSince := h.DownSince
	h.State = "online"
	h.LastSuccess = now
	h.LastMessage = ""
	h.DownSince = time.Time{}
	h.LastNotification = time.Time{}
	_ = a.state.PutHealth(h)
	if wasDown && wasNotified {
		duration := now.Sub(downSince).Round(time.Second)
		go a.notify("backend_recovered", "MoonDav backend recovered", "Backend connectivity was restored after "+duration.String()+". Queued reading progress will be synchronized automatically.")
	}
}

func (a *App) reconcileLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	a.reconcile()
	for range ticker.C {
		a.reconcile()
		h := a.state.Health()
		if h.State == "offline" || h.State == "error" {
			a.maybeNotifyOutage(h)
		}
	}
}

func (a *App) reconcile() {
	now := time.Now().UTC()
	for key, entry := range a.state.Snapshot() {
		id, mapped := a.bookMap.Resolve(key)
		if !mapped {
			continue
		}

		if entry.PendingSync || entry.Percent > entry.BackendPercent+0.01 {
			if entry.NextRetryAt.IsZero() || !now.Before(entry.NextRetryAt) {
				a.push(key)
			}
			continue
		}

		remote, updated, err := a.backend.Pull(id)
		if err != nil {
			if temporary(err) {
				a.backendFailure("offline", err.Error())
			} else {
				entry.LastError = err.Error()
				_ = a.state.Put(key, entry)
				a.backendFailure("error", err.Error())
			}
			continue
		}
		a.backendSuccess()
		entry.LastError = ""
		if remote > entry.Percent+0.01 {
			entry.BackendPercent = remote
			entry.BackendUpdatedAt = updated
			entry.RemoteAhead = entry.SuppressRemoteUntilPct+0.01 < remote
		} else {
			entry.RemoteAhead = false
			entry.BackendPercent = remote
			entry.BackendUpdatedAt = updated
		}
		_ = a.state.Put(key, entry)
	}
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"backend":   a.cfg.BackendType,
		"base_path": a.cfg.BasePath,
		"entries":   a.state.Snapshot(),
		"backend_health": a.state.Health(),
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
