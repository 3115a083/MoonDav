package moondav

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Listen:         ":8765",
		DataDir:        dir,
		DAVUser:        "moon",
		DAVPassword:    "dav-secret",
		AdminUser:      "admin",
		AdminPassword:  "admin-secret",
		BasePath:       "/dav/",
		MaxUploadBytes: 8 << 20,
		ConflictPolicy: "furthest",
		BackendType:    "none",
		BookMapFile:    filepath.Join(dir, "book-map.json"),
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func request(t *testing.T, h http.Handler, method, path, body, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminAndDAVCredentialsAreSeparated(t *testing.T) {
	h := testApp(t).Handler()

	if got := request(t, h, http.MethodGet, "/", "", "moon", "dav-secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("DAV credentials must not open admin UI: got %d", got)
	}
	if got := request(t, h, http.MethodGet, "/", "", "admin", "admin-secret").Code; got != http.StatusOK {
		t.Fatalf("admin UI should open: got %d", got)
	}
	if got := request(t, h, http.MethodOptions, "/dav/", "", "admin", "admin-secret").Code; got != http.StatusUnauthorized {
		t.Fatalf("admin credentials must not open WebDAV: got %d", got)
	}
}

func TestDashboardAssetsAndMappingWrite(t *testing.T) {
	app := testApp(t)
	h := app.Handler()

	if got := request(t, h, http.MethodGet, "/assets/app.css", "", "admin", "admin-secret").Code; got != http.StatusOK {
		t.Fatalf("asset should be available: got %d", got)
	}

	payload, _ := json.Marshal(map[string]string{
		"book_key": "Example.epub",
		"backend_id": "uuid-123",
	})
	rec := request(t, h, http.MethodPost, "/api/mappings", string(payload), "admin", "admin-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("mapping write failed: %d %s", rec.Code, rec.Body.String())
	}
	if got, ok := app.bookMap.Resolve("example.epub"); !ok || got != "uuid-123" {
		t.Fatalf("mapping not persisted in memory: %q %v", got, ok)
	}

	reopened, err := OpenBookMap(filepath.Join(app.cfg.DataDir, "book-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Resolve("example.epub"); !ok || got != "uuid-123" {
		t.Fatalf("mapping not persisted on disk: %q %v", got, ok)
	}
}
