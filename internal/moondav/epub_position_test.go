package moondav

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestMoonPositionCodecRoundTrip(t *testing.T) {
	raw := []byte("1703297605115*21@0#4826:11.1%")
	p := ParseMoonPosition(raw)
	if !p.Valid || p.TimestampMS != 1703297605115 || p.Chapter != 21 ||
		p.Section != 0 || p.Offset != 4826 || p.Percent != 11.1 {
		t.Fatalf("unexpected Moon+ decode: %+v", p)
	}
	encoded, err := EncodeMoonPosition(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(raw) {
		t.Fatalf("round trip mismatch: %q", encoded)
	}
}

func TestExactEPUBKoboRoundTrip(t *testing.T) {
	root := t.TempDir()
	epub := filepath.Join(root, "book.epub")
	writeTestEPUB(t, epub)

	bookMap, err := OpenBookMap(filepath.Join(t.TempDir(), "book-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bookMap.SetDetailed("book.epub", BookMapping{
		BackendID: "uuid-1",
		EPUBPath: "book.epub",
	}); err != nil {
		t.Fatal(err)
	}

	app := &App{
		cfg: Config{LibraryRoot: root, ExactPositions: true},
		bookMap: bookMap,
	}
	base := MoonPosition{
		TimestampMS: 1703297605115,
		Chapter: 0,
		Section: 0,
		Offset: 13,
		Percent: 42.5,
		Valid: true,
	}
	loc, err := app.moonToKobo("book.epub", base)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Type != "KoboSpan" || loc.Value == "" || loc.Source != "text/ch1.xhtml" {
		t.Fatalf("unexpected Kobo location: %+v", loc)
	}
	back, err := app.koboToMoon("book.epub", *loc, 43.0, base)
	if err != nil {
		t.Fatal(err)
	}
	if back.TimestampMS != base.TimestampMS || back.Chapter != 0 || back.Section != 0 {
		t.Fatalf("Moon+ identity was not preserved: %+v", back)
	}
	if back.Offset < 0 || back.Offset > base.Offset {
		t.Fatalf("KoboSpan should resolve to a span start at/before the original offset: %+v", back)
	}
	if back.Percent != 43.0 {
		t.Fatalf("remote percent not applied: %+v", back)
	}
}

func TestEPUBPathCannotEscapeLibraryRoot(t *testing.T) {
	app := &App{cfg: Config{LibraryRoot: t.TempDir()}}
	if _, err := app.safeLibraryPath("../secret.epub"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, err := app.safeLibraryPath("/etc/passwd"); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
}

func writeTestEPUB(t *testing.T, filename string) {
	t.Helper()
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		"OEBPS/text/ch1.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>One</title></head><body><p>Hello world. This is a test sentence. Another sentence follows.</p></body></html>`,
		"OEBPS/text/ch2.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Two</title></head><body><p>Second chapter.</p></body></html>`,
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
