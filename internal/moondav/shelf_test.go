package moondav

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemShelfIsReadOnlyAndSupportsRange(t *testing.T) {
	root := t.TempDir()
	book := filepath.Join(root, "Example.epub")
	content := []byte("0123456789abcdef")
	if err := os.WriteFile(book, content, 0644); err != nil {
		t.Fatal(err)
	}

	app := testApp(t)
	app.cfg.ShelfMode = "filesystem"
	app.cfg.ShelfRoot = root
	h := app.Handler()

	rec := request(t, h, http.MethodGet, "/opds/", "", "moon", "dav-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Example") || !strings.Contains(rec.Body.String(), "http://opds-spec.org/acquisition") {
		t.Fatalf("catalog does not contain acquisition entry: %s", rec.Body.String())
	}

	files, err := app.scanShelfFiles()
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one indexed shelf file: %+v %v", files, err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(files[0].ID))
	req := httptest.NewRequest(http.MethodGet, "/opds/file?id="+token, nil)
	req.SetBasicAuth("moon", "dav-secret")
	req.Header.Set("Range", "bytes=2-5")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != http.StatusPartialContent {
		t.Fatalf("range request failed: %d %s", out.Code, out.Body.String())
	}
	if out.Body.String() != "2345" {
		t.Fatalf("unexpected range body: %q", out.Body.String())
	}

	put := request(t, h, http.MethodPut, "/opds/file?id="+token, "damage", "moon", "dav-secret")
	if put.Code != http.StatusMethodNotAllowed {
		t.Fatalf("shelf must reject writes, got %d", put.Code)
	}
	after, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(content) {
		t.Fatal("source file changed after shelf requests")
	}
}

func TestFilesystemShelfSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.epub")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.epub")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	app := testApp(t)
	app.cfg.ShelfMode = "filesystem"
	app.cfg.ShelfRoot = root
	files, err := app.scanShelfFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("symlink must not be indexed: %+v", files)
	}
}

func TestOPDSProxyRewritesAndStreamsWithoutCaching(t *testing.T) {
	var sawRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "upstream" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/opds":
			w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
			_, _ = io.WriteString(w, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><id>x</id><title>x</title><entry><id>urn:uuid:1</id><title>Book</title><link rel="http://opds-spec.org/acquisition" href="/download/1" type="application/epub+zip"/></entry></feed>`)
		case "/download/1":
			sawRange = r.Header.Get("Range")
			w.Header().Set("Content-Type", "application/epub+zip")
			w.Header().Set("ETag", `"abc"`)
			if sawRange != "" {
				w.Header().Set("Content-Range", "bytes 1-3/6")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "bcd")
				return
			}
			_, _ = io.WriteString(w, "abcdef")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	app := testApp(t)
	app.cfg.ShelfMode = "opds"
	app.cfg.ShelfURL = upstream.URL + "/opds"
	app.cfg.ShelfUser = "upstream"
	app.cfg.ShelfPassword = "secret"
	app.cfg.ShelfMaxFeedBytes = 1 << 20
	h := app.Handler()

	catalog := request(t, h, http.MethodGet, "/opds/", "", "moon", "dav-secret")
	if catalog.Code != http.StatusOK {
		t.Fatalf("proxy catalog failed: %d %s", catalog.Code, catalog.Body.String())
	}
	body := catalog.Body.String()
	i := strings.Index(body, "/opds/proxy?u=")
	if i < 0 {
		t.Fatalf("acquisition link was not rewritten: %s", body)
	}
	start := i + len("/opds/proxy?u=")
	end := strings.IndexAny(body[start:], `"&<`)
	if end < 0 {
		t.Fatalf("could not parse rewritten link: %s", body)
	}
	token := body[start : start+end]
	token = strings.ReplaceAll(token, "&amp;", "&")

	req := httptest.NewRequest(http.MethodGet, "/opds/proxy?u="+token, nil)
	req.SetBasicAuth("moon", "dav-secret")
	req.Header.Set("Range", "bytes=1-3")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != http.StatusPartialContent || out.Body.String() != "bcd" {
		t.Fatalf("proxied range failed: %d %q", out.Code, out.Body.String())
	}
	if sawRange != "bytes=1-3" {
		t.Fatalf("Range was not forwarded: %q", sawRange)
	}
	if out.Header().Get("ETag") != `"abc"` {
		t.Fatalf("ETag was not preserved: %q", out.Header().Get("ETag"))
	}

	dataRoot := app.cfg.DataDir
	entries, err := os.ReadDir(filepath.Join(dataRoot, "webdav"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil && len(entries) != 0 {
		t.Fatalf("proxy must not cache book files in WebDAV storage: %+v", entries)
	}
}


func TestOPDSProxyRejectsCrossOriginRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "should not be reached")
	}))
	defer evil.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/book.epub", http.StatusFound)
	}))
	defer upstream.Close()

	app := testApp(t)
	app.cfg.ShelfMode = "opds"
	app.cfg.ShelfURL = upstream.URL
	app.cfg.ShelfMaxFeedBytes = 1 << 20

	rec := request(t, app.Handler(), http.MethodGet, "/opds/", "", "moon", "dav-secret")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("cross-origin redirect must fail, got %d", rec.Code)
	}
}

func TestOPDSProxyRejectsOtherOrigins(t *testing.T) {
	app := testApp(t)
	app.cfg.ShelfMode = "opds"
	app.cfg.ShelfURL = "https://books.example/opds"
	token := base64.RawURLEncoding.EncodeToString([]byte("https://evil.example/secret"))
	req := httptest.NewRequest(http.MethodGet, "/opds/proxy?u="+token, nil)
	req.SetBasicAuth("moon", "dav-secret")
	out := httptest.NewRecorder()
	app.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusBadRequest {
		t.Fatalf("cross-origin proxy should be rejected, got %d", out.Code)
	}
}
