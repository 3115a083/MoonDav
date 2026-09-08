package moondav

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type switchBackend struct {
	fail bool
	last float64
}

func (b *switchBackend) Push(_ string, p float64) error {
	if b.fail {
		return &BackendError{Temporary: true, Message: "backend offline"}
	}
	b.last = p
	return nil
}

func (b *switchBackend) Pull(string) (float64, time.Time, error) {
	if b.fail {
		return 0, time.Time{}, &BackendError{Temporary: true, Message: "backend offline"}
	}
	return b.last, time.Now().UTC(), nil
}

func TestTemporaryBackendFailureKeepsLatestProgressQueued(t *testing.T) {
	dir := t.TempDir()
	state, err := OpenState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	bookMap, err := OpenBookMap(filepath.Join(dir, "book-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bookMap.Set("book.epub", "backend-id"); err != nil {
		t.Fatal(err)
	}

	be := &switchBackend{fail: true}
	app := &App{
		cfg: Config{
			DataDir: dir, BackendType: "calibre-web",
			NotifyAfter: time.Hour, NotifyRepeat: time.Hour,
		},
		state: state, backend: be, bookMap: bookMap,
	}

	entry := StateEntry{
		Percent: 42.5, PendingSync: true, PendingPercent: 42.5,
		NextRetryAt: time.Now().Add(-time.Second),
	}
	if err := state.Put("book.epub", entry); err != nil {
		t.Fatal(err)
	}

	app.push("book.epub")

	got, _ := state.Get("book.epub")
	if !got.PendingSync || got.PendingPercent != 42.5 {
		t.Fatalf("progress was not queued: %+v", got)
	}
	if got.LastError != "" {
		t.Fatalf("temporary outage must not become a book error: %q", got.LastError)
	}
	if state.Health().State != "offline" {
		t.Fatalf("backend health should be offline: %+v", state.Health())
	}

	be.fail = false
	got.NextRetryAt = time.Now().Add(-time.Second)
	if err := state.Put("book.epub", got); err != nil {
		t.Fatal(err)
	}
	app.push("book.epub")

	got, _ = state.Get("book.epub")
	if got.PendingSync {
		t.Fatalf("queue should clear after recovery: %+v", got)
	}
	if got.BackendPercent != 42.5 || be.last != 42.5 {
		t.Fatalf("queued progress was not delivered: state=%+v backend=%v", got, be.last)
	}
	if state.Health().State != "online" {
		t.Fatalf("backend health should recover: %+v", state.Health())
	}
}

func TestSecretEnvPrefersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET", "from-env")
	t.Setenv("TEST_SECRET_FILE", path)
	if got := secretEnv("TEST_SECRET"); got != "from-file" {
		t.Fatalf("expected file secret, got %q", got)
	}
}

func TestBackendErrorClassification(t *testing.T) {
	if !temporary(&BackendError{Temporary: true, Message: "down"}) {
		t.Fatal("temporary backend error not recognized")
	}
	if temporary(errors.New("bad config")) {
		t.Fatal("plain errors must not be treated as temporary")
	}
}
