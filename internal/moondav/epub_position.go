package moondav

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pgaskin/kepubify/v4/kepub"
	"golang.org/x/net/html"
)

type ReadingLocation struct {
	Value  string `json:"Value"`
	Type   string `json:"Type"`
	Source string `json:"Source"`
}

type epubPackage struct {
	OPFPath string
	Items   map[string]string
	Spine   []string
}

type koboSpan struct {
	ID    string
	Start int64
	End   int64
}

func (a *App) moonToKobo(bookKey string, pos MoonPosition) (*ReadingLocation, error) {
	if !a.cfg.ExactPositions || a.cfg.LibraryRoot == "" || !pos.Valid {
		return nil, errors.New("exact translation disabled")
	}
	m, ok := a.bookMap.ResolveMapping(bookKey)
	if !ok || m.EPUBPath == "" {
		return nil, errors.New("no EPUB source mapping")
	}
	epubPath, err := a.safeLibraryPath(m.EPUBPath)
	if err != nil {
		return nil, err
	}
	pkg, zr, closeFn, err := openEPUB(epubPath)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	if pos.Chapter < 0 || pos.Chapter >= len(pkg.Spine) {
		return nil, fmt.Errorf("Moon+ chapter %d outside EPUB spine", pos.Chapter)
	}
	itemID := pkg.Spine[pos.Chapter]
	href, ok := pkg.Items[itemID]
	if !ok {
		return nil, fmt.Errorf("spine item %q missing from manifest", itemID)
	}
	full := resolveEPUBPath(pkg.OPFPath, href)
	raw, err := readZipFile(zr, full)
	if err != nil {
		return nil, err
	}
	spans, _, err := koboSpans(raw)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, errors.New("kepubify produced no KoboSpan markers")
	}
	target := spans[len(spans)-1]
	for _, sp := range spans {
		if pos.Offset < sp.End {
			target = sp
			break
		}
	}
	return &ReadingLocation{
		Value: target.ID,
		Type: "KoboSpan",
		Source: normalizeHref(href),
	}, nil
}

func (a *App) koboToMoon(bookKey string, loc ReadingLocation, percent float64, base MoonPosition) (MoonPosition, error) {
	if !a.cfg.ExactPositions || a.cfg.LibraryRoot == "" {
		return MoonPosition{}, errors.New("exact translation disabled")
	}
	if !base.Valid || base.TimestampMS <= 0 {
		return MoonPosition{}, errors.New("original Moon+ position is required to preserve its timestamp")
	}
	if loc.Type != "KoboSpan" || loc.Value == "" || loc.Source == "" {
		return MoonPosition{}, errors.New("unsupported Kobo location")
	}
	m, ok := a.bookMap.ResolveMapping(bookKey)
	if !ok || m.EPUBPath == "" {
		return MoonPosition{}, errors.New("no EPUB source mapping")
	}
	epubPath, err := a.safeLibraryPath(m.EPUBPath)
	if err != nil {
		return MoonPosition{}, err
	}
	pkg, zr, closeFn, err := openEPUB(epubPath)
	if err != nil {
		return MoonPosition{}, err
	}
	defer closeFn()

	source := normalizeHref(loc.Source)
	chapter := -1
	var href string
	for i, itemID := range pkg.Spine {
		h := pkg.Items[itemID]
		if normalizeHref(h) == source {
			chapter = i
			href = h
			break
		}
	}
	if chapter < 0 {
		return MoonPosition{}, fmt.Errorf("Kobo source %q is not in EPUB spine", source)
	}
	raw, err := readZipFile(zr, resolveEPUBPath(pkg.OPFPath, href))
	if err != nil {
		return MoonPosition{}, err
	}
	spans, _, err := koboSpans(raw)
	if err != nil {
		return MoonPosition{}, err
	}
	for _, sp := range spans {
		if sp.ID == loc.Value {
			return MoonPosition{
				TimestampMS: base.TimestampMS,
				Chapter: chapter,
				Section: base.Section,
				Offset: sp.Start,
				Percent: percent,
				Valid: true,
			}, nil
		}
	}
	return MoonPosition{}, fmt.Errorf("KoboSpan %q not found in %q", loc.Value, source)
}


func (a *App) readStoredMoonPosition(entry StateEntry) (MoonPosition, error) {
	local, err := a.localDAVPath(entry.Path)
	if err != nil {
		return MoonPosition{}, err
	}
	b, err := os.ReadFile(local)
	if err != nil {
		return MoonPosition{}, err
	}
	p := ParseMoonPosition(b)
	if !p.Valid {
		return MoonPosition{}, errors.New("stored Moon+ position is invalid")
	}
	return p, nil
}

func (a *App) writeStoredMoonPosition(entry StateEntry, p MoonPosition) error {
	local, err := a.localDAVPath(entry.Path)
	if err != nil {
		return err
	}
	b, err := EncodeMoonPosition(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		return err
	}
	tmp := local + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, local)
}

func (a *App) localDAVPath(requestPath string) (string, error) {
	p := filepath.ToSlash(requestPath)
	base := filepath.ToSlash(a.cfg.BasePath)
	if !strings.HasPrefix(p, base) {
		return "", errors.New("position path is outside WebDAV base")
	}
	rel := strings.TrimPrefix(p, base)
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid WebDAV position path")
	}
	root := filepath.Join(a.cfg.DataDir, "webdav")
	full := filepath.Join(root, clean)
	r, err := filepath.Rel(root, full)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("WebDAV position path escapes data root")
	}
	return full, nil
}

func (a *App) safeLibraryPath(rel string) (string, error) {
	if a.cfg.LibraryRoot == "" {
		return "", errors.New("MOONDAV_LIBRARY_ROOT is not configured")
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("epub_path must be relative to MOONDAV_LIBRARY_ROOT")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid epub_path")
	}
	root, err := filepath.Abs(a.cfg.LibraryRoot)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("epub_path escapes library root")
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("epub_path points to a directory")
	}
	return full, nil
}

func openEPUB(filename string) (epubPackage, *zip.Reader, func(), error) {
	f, err := os.Open(filename)
	if err != nil {
		return epubPackage{}, nil, func(){}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return epubPackage{}, nil, func(){}, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		f.Close()
		return epubPackage{}, nil, func(){}, err
	}
	container, err := readZipFile(zr, "META-INF/container.xml")
	if err != nil {
		f.Close()
		return epubPackage{}, nil, func(){}, err
	}
	var cx struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &cx); err != nil || len(cx.Rootfiles) == 0 {
		f.Close()
		return epubPackage{}, nil, func(){}, errors.New("invalid EPUB container.xml")
	}
	opfPath := path.Clean(cx.Rootfiles[0].FullPath)
	opf, err := readZipFile(zr, opfPath)
	if err != nil {
		f.Close()
		return epubPackage{}, nil, func(){}, err
	}
	var px struct {
		Manifest struct {
			Items []struct {
				ID   string `xml:"id,attr"`
				Href string `xml:"href,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			Items []struct {
				IDRef string `xml:"idref,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.Unmarshal(opf, &px); err != nil {
		f.Close()
		return epubPackage{}, nil, func(){}, fmt.Errorf("invalid EPUB package: %w", err)
	}
	pkg := epubPackage{OPFPath: opfPath, Items: map[string]string{}}
	for _, it := range px.Manifest.Items {
		pkg.Items[it.ID] = normalizeHref(it.Href)
	}
	for _, it := range px.Spine.Items {
		pkg.Spine = append(pkg.Spine, it.IDRef)
	}
	if len(pkg.Spine) == 0 {
		f.Close()
		return epubPackage{}, nil, func(){}, errors.New("EPUB has empty spine")
	}
	return pkg, zr, func(){ _ = f.Close() }, nil
}

func koboSpans(raw []byte) ([]koboSpan, int64, error) {
	var transformed bytes.Buffer
	if err := kepub.NewConverter().TransformContent(&transformed, bytes.NewReader(raw)); err != nil {
		return nil, 0, err
	}
	doc, err := html.Parse(bytes.NewReader(transformed.Bytes()))
	if err != nil {
		return nil, 0, err
	}
	var spans []koboSpan
	var offset int64
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, ignored bool) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "head":
				ignored = true
			}
		}
		if ignored {
			return
		}
		var current *koboSpan
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "span") {
			var id string
			var isKobo bool
			for _, a := range n.Attr {
				if a.Key == "id" && strings.HasPrefix(a.Val, "kobo.") {
					id = a.Val
				}
				if a.Key == "class" {
					for _, cls := range strings.Fields(a.Val) {
						if cls == "koboSpan" {
							isKobo = true
						}
					}
				}
			}
			if isKobo && id != "" {
				spans = append(spans, koboSpan{ID: id, Start: offset})
				current = &spans[len(spans)-1]
			}
		}
		if n.Type == html.TextNode {
			offset += int64(utf8.RuneCountInString(n.Data))
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, ignored)
		}
		if current != nil {
			current.End = offset
		}
	}
	walk(doc, false)
	for i := range spans {
		if spans[i].End == 0 {
			if i+1 < len(spans) {
				spans[i].End = spans[i+1].Start
			} else {
				spans[i].End = offset
			}
		}
	}
	return spans, offset, nil
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = strings.TrimPrefix(path.Clean(filepath.ToSlash(name)), "/")
	for _, f := range zr.File {
		if path.Clean(f.Name) != name {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(io.LimitReader(r, 32<<20))
	}
	return nil, fmt.Errorf("EPUB file %q not found", name)
}

func resolveEPUBPath(opfPath, href string) string {
	href = normalizeHref(href)
	return path.Clean(path.Join(path.Dir(opfPath), href))
}

func normalizeHref(href string) string {
	if u, err := url.PathUnescape(strings.SplitN(href, "#", 2)[0]); err == nil {
		href = u
	}
	return strings.TrimPrefix(path.Clean(filepath.ToSlash(href)), "./")
}

func parseKoboID(v string) (int, int, bool) {
	var major, minor int
	if _, err := fmt.Sscanf(v, "kobo.%d.%d", &major, &minor); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func formatKoboID(major, minor int) string {
	return "kobo." + strconv.Itoa(major) + "." + strconv.Itoa(minor)
}
