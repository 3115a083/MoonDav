package moondav

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var shelfLinkRE = regexp.MustCompile(`(?i)(href|template)\s*=\s*"([^"]+)"`)

type shelfFile struct {
	Rel     string
	Title   string
	Size    int64
	ModTime time.Time
	MIME    string
	ID      string
}

type shelfIndex struct {
	mu    sync.Mutex
	root  string
	at    time.Time
	files []shelfFile
}

type shelfProxyLink struct {
	URL       string
	ExpiresAt time.Time
}

type shelfProxyRegistry struct {
	mu    sync.Mutex
	links map[string]shelfProxyLink
}

func (a *App) shelfHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "shelf is read-only", http.StatusMethodNotAllowed)
			return
		}
		switch a.cfg.ShelfMode {
		case "opds":
			a.proxyOPDS(w, r)
		case "filesystem":
			a.filesystemOPDS(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func (a *App) proxyOPDS(w http.ResponseWriter, r *http.Request) {
	target, err := a.shelfTargetURL(r)
	if err != nil {
		http.Error(w, "invalid shelf target", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadGateway)
		return
	}
	if a.cfg.ShelfUser != "" || a.cfg.ShelfPassword != "" {
		req.SetBasicAuth(a.cfg.ShelfUser, a.cfg.ShelfPassword)
	}
	for _, h := range []string{"Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Range"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("User-Agent", "MoonDav/OPDS")
	origin, _ := url.Parse(a.cfg.ShelfURL)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout: 90 * time.Second,
		},
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if origin == nil || next.URL.Scheme != origin.Scheme || !strings.EqualFold(next.URL.Host, origin.Host) {
				return fmt.Errorf("redirect leaves configured OPDS origin")
			}
			if a.cfg.ShelfUser != "" || a.cfg.ShelfPassword != "" {
				next.SetBasicAuth(a.cfg.ShelfUser, a.cfg.ShelfPassword)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "shelf source unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, h := range []string{
		"Content-Type", "Content-Disposition", "ETag", "Last-Modified",
		"Accept-Ranges", "Content-Range", "Cache-Control",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "xml") || strings.Contains(contentType, "atom") || strings.Contains(contentType, "opds") {
		body, err := io.ReadAll(io.LimitReader(resp.Body, a.cfg.ShelfMaxFeedBytes+1))
		if err != nil {
			http.Error(w, "could not read shelf catalog", http.StatusBadGateway)
			return
		}
		if int64(len(body)) > a.cfg.ShelfMaxFeedBytes {
			http.Error(w, "shelf catalog exceeds size limit", http.StatusBadGateway)
			return
		}
		rewritten := a.rewriteOPDSLinks(body, target)
		w.Header().Del("Content-Length")
		w.WriteHeader(resp.StatusCode)
		if r.Method != http.MethodHead {
			_, _ = w.Write(rewritten)
		}
		return
	}

	if v := resp.Header.Get("Content-Length"); v != "" {
		w.Header().Set("Content-Length", v)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func (a *App) shelfTargetURL(r *http.Request) (*url.URL, error) {
	base, err := url.Parse(a.cfg.ShelfURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid shelf URL")
	}
	token := r.URL.Query().Get("t")
	if token == "" {
		if r.URL.Path != "/opds" && r.URL.Path != "/opds/" {
			return nil, fmt.Errorf("missing target")
		}
		return base, nil
	}
	return a.resolveShelfLink(token)
}

func (a *App) registerShelfLink(target *url.URL) (string, bool) {
	base, err := url.Parse(a.cfg.ShelfURL)
	if err != nil || target == nil || target.User != nil ||
		target.Scheme != base.Scheme || !strings.EqualFold(target.Host, base.Host) {
		return "", false
	}
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	now := time.Now()
	a.shelfProxy.mu.Lock()
	defer a.shelfProxy.mu.Unlock()
	if a.shelfProxy.links == nil {
		a.shelfProxy.links = make(map[string]shelfProxyLink)
	}
	for key, link := range a.shelfProxy.links {
		if now.After(link.ExpiresAt) {
			delete(a.shelfProxy.links, key)
		}
	}
	if len(a.shelfProxy.links) >= 10000 {
		a.shelfProxy.links = make(map[string]shelfProxyLink)
	}
	a.shelfProxy.links[token] = shelfProxyLink{
		URL: target.String(),
		ExpiresAt: now.Add(30 * time.Minute),
	}
	return token, true
}

func (a *App) resolveShelfLink(token string) (*url.URL, error) {
	if len(token) < 16 || len(token) > 64 {
		return nil, fmt.Errorf("invalid shelf token")
	}
	a.shelfProxy.mu.Lock()
	link, ok := a.shelfProxy.links[token]
	if ok && time.Now().After(link.ExpiresAt) {
		delete(a.shelfProxy.links, token)
		ok = false
	}
	a.shelfProxy.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown or expired shelf token")
	}
	target, err := url.Parse(link.URL)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(a.cfg.ShelfURL)
	if err != nil || target.User != nil ||
		target.Scheme != base.Scheme || !strings.EqualFold(target.Host, base.Host) {
		return nil, fmt.Errorf("registered target leaves configured OPDS origin")
	}
	return target, nil
}

func (a *App) rewriteOPDSLinks(body []byte, current *url.URL) []byte {
	return shelfLinkRE.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := shelfLinkRE.FindSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		raw := html.UnescapeString(string(parts[2]))
		if strings.Contains(raw, "{") {
			return match
		}
		ref, err := url.Parse(raw)
		if err != nil {
			return match
		}
		resolved := current.ResolveReference(ref)
		base, err := url.Parse(a.cfg.ShelfURL)
		if err != nil || resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Host, base.Host) {
			return match
		}
		token, ok := a.registerShelfLink(resolved)
		if !ok {
			return match
		}
		replacement := "/opds/proxy?t=" + url.QueryEscape(token)
		return []byte(fmt.Sprintf(`%s="%s"`, parts[1], html.EscapeString(replacement)))
	})
}

func (a *App) filesystemOPDS(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/opds/file" {
		a.serveShelfFile(w, r)
		return
	}
	if r.URL.Path != "/opds" && r.URL.Path != "/opds/" {
		http.NotFound(w, r)
		return
	}
	files, err := a.scanShelfFiles()
	if err != nil {
		http.Error(w, "shelf source unavailable", http.StatusServiceUnavailable)
		return
	}

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q != "" {
		filtered := files[:0]
		for _, f := range files {
			if strings.Contains(strings.ToLower(f.Title), q) || strings.Contains(strings.ToLower(f.Rel), q) {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	page := positiveInt(r.URL.Query().Get("page"), 1)
	size := positiveInt(r.URL.Query().Get("size"), 100)
	if size > 200 {
		size = 200
	}
	start := (page - 1) * size
	if start > len(files) {
		start = len(files)
	}
	end := start + size
	if end > len(files) {
		end = len(files)
	}

	w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog;kind=acquisition; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/2010/catalog">`)
	b.WriteString("<id>urn:moondav:shelf</id><title>MoonDav Shelf</title>")
	b.WriteString("<updated>" + time.Now().UTC().Format(time.RFC3339) + "</updated>")
	b.WriteString(`<link rel="self" href="/opds/" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`)
	if end < len(files) {
		next := fmt.Sprintf("/opds/?page=%d&size=%d", page+1, size)
		if q != "" {
			next += "&q=" + url.QueryEscape(q)
		}
		b.WriteString(`<link rel="next" href="` + xmlEscape(next) + `" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>`)
	}
	for _, f := range files[start:end] {
		href := "/opds/file?id=" + url.QueryEscape(f.ID)
		b.WriteString("<entry>")
		b.WriteString("<id>" + xmlEscape(f.ID) + "</id>")
		b.WriteString("<title>" + xmlEscape(f.Title) + "</title>")
		b.WriteString("<updated>" + f.ModTime.UTC().Format(time.RFC3339) + "</updated>")
		b.WriteString(`<link rel="http://opds-spec.org/acquisition" href="` + xmlEscape(href) + `" type="` + xmlEscape(f.MIME) + `" length="` + strconv.FormatInt(f.Size, 10) + `"/>`)
		b.WriteString("</entry>")
	}
	b.WriteString("</feed>")
	_, _ = io.WriteString(w, b.String())
}

func (a *App) scanShelfFiles() ([]shelfFile, error) {
	root, err := filepath.Abs(a.cfg.ShelfRoot)
	if err != nil {
		return nil, err
	}
	a.shelfIndex.mu.Lock()
	defer a.shelfIndex.mu.Unlock()
	if a.shelfIndex.root == root && time.Since(a.shelfIndex.at) < 30*time.Second {
		return append([]shelfFile(nil), a.shelfIndex.files...), nil
	}
	var out []shelfFile
	err = filepath.WalkDir(root, func(full string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if len(out) >= 10000 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		mimeType, ok := shelfMIME(ext)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		sum := sha256.Sum256([]byte(rel))
		out = append(out, shelfFile{
			Rel: rel,
			Title: strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)),
			Size: info.Size(),
			ModTime: info.ModTime(),
			MIME: mimeType,
			ID: fmt.Sprintf("urn:moondav:file:%x", sum[:16]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	a.shelfIndex.root = root
	a.shelfIndex.at = time.Now()
	a.shelfIndex.files = append([]shelfFile(nil), out...)
	return append([]shelfFile(nil), out...), nil
}

func (a *App) serveShelfFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	files, err := a.scanShelfFiles()
	if err != nil {
		http.Error(w, "shelf source unavailable", http.StatusServiceUnavailable)
		return
	}
	var selected *shelfFile
	for i := range files {
		if files[i].ID == id {
			selected = &files[i]
			break
		}
	}
	if selected == nil {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.Abs(a.cfg.ShelfRoot)
	if err != nil {
		http.Error(w, "invalid shelf root", http.StatusInternalServerError)
		return
	}
	rel := filepath.Clean(filepath.FromSlash(selected.Rel))
	full := filepath.Join(root, rel)
	actual, err := filepath.Abs(full)
	if err != nil {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	check, err := filepath.Rel(root, actual)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		http.Error(w, "invalid file reference", http.StatusBadRequest)
		return
	}
	linfo, err := os.Lstat(actual)
	if err != nil || linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(actual)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "could not stat source file", http.StatusInternalServerError)
		return
	}
	mimeType, ok := shelfMIME(strings.ToLower(filepath.Ext(actual)))
	if !ok {
		http.Error(w, "unsupported format", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(actual)})
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(actual), info.ModTime(), f)
}

func shelfMIME(ext string) (string, bool) {
	switch ext {
	case ".epub":
		return "application/epub+zip", true
	case ".pdf":
		return "application/pdf", true
	case ".mobi":
		return "application/x-mobipocket-ebook", true
	case ".azw", ".azw3":
		return "application/vnd.amazon.ebook", true
	case ".fb2":
		return "application/x-fictionbook+xml", true
	case ".cbz":
		return "application/vnd.comicbook+zip", true
	case ".cbr":
		return "application/vnd.comicbook-rar", true
	default:
		if v := mime.TypeByExtension(ext); v != "" {
			return v, false
		}
		return "", false
	}
}

func positiveInt(v string, d int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return d
	}
	return n
}

func xmlEscape(v string) string {
	return html.EscapeString(v)
}
