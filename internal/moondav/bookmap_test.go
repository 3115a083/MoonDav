package moondav

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBookMapReadsLegacyStringEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book-map.json")
	data := []byte(`{"entries":{"book.epub":"uuid-legacy"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := OpenBookMap(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.ResolveMapping("book.epub")
	if !ok || got.BackendID != "uuid-legacy" || got.EPUBPath != "" {
		t.Fatalf("legacy mapping not preserved: %+v %v", got, ok)
	}
}

func TestBookMapWritesDetailedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book-map.json")
	m, err := OpenBookMap(path)
	if err != nil {
		t.Fatal(err)
	}
	want := BookMapping{BackendID: "uuid-1", EPUBPath: "Author/Book/book.epub"}
	if err := m.SetDetailed("Book.epub", want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Entries map[string]BookMapping `json:"entries"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Entries["book.epub"]; got != want {
		t.Fatalf("detailed mapping mismatch: got %+v want %+v", got, want)
	}
}
