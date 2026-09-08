package moondav

import "testing"

func TestParseMoonPosition(t *testing.T) {
	p := ParseMoonPosition([]byte("1703297605115*21@0#4826:11.1%"))
	if !p.Valid || p.Percent != 11.1 {
		t.Fatalf("unexpected parse: %+v", p)
	}
}

func TestPositionPath(t *testing.T) {
	if !IsPositionPath("/dav/Books/.Moon+/Cache/example.epub.po") {
		t.Fatal("expected Moon+ position path")
	}
	if IsPositionPath("/dav/Books/example.epub") {
		t.Fatal("ebook must not be treated as a position file")
	}
}
