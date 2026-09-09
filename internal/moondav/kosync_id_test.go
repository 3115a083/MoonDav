package moondav

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKOReaderPartialMD5MatchesReference(t *testing.T) {
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte((i*37 + 11) % 256)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := koreaderPartialMD5(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "efc404fa609a799c7801273de6d65e84" {
		t.Fatalf("unexpected partial MD5: %s", got)
	}
}

func TestCWAUsesEPUBPartialMD5AsBackendID(t *testing.T) {
	root := t.TempDir()
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte((i*37 + 11) % 256)
	}
	if err := os.WriteFile(filepath.Join(root, "book.epub"), data, 0600); err != nil {
		t.Fatal(err)
	}

	app := testApp(t)
	app.cfg.BackendType = "cwa"
	app.cfg.LibraryRoot = root
	if err := app.bookMap.SetDetailed("book.epub", BookMapping{
		BackendID: "legacy-or-calibre-id",
		EPUBPath:  "book.epub",
	}); err != nil {
		t.Fatal(err)
	}

	got, ok := app.resolveBackendID("book.epub")
	if !ok {
		t.Fatal("expected backend ID")
	}
	if got != "efc404fa609a799c7801273de6d65e84" {
		t.Fatalf("expected KOReader checksum, got %q", got)
	}
}

func TestCWAFallsBackToConfiguredBackendID(t *testing.T) {
	app := testApp(t)
	app.cfg.BackendType = "calibre-web-automated"
	if err := app.bookMap.SetDetailed("book.epub", BookMapping{
		BackendID: "42",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := app.resolveBackendID("book.epub")
	if !ok || got != "42" {
		t.Fatalf("unexpected fallback ID: %q %v", got, ok)
	}
}
