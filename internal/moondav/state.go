package moondav

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type StateEntry struct {
	Path                   string    `json:"path"`
	Percent                float64   `json:"percent"`
	UpdatedAt              time.Time `json:"updated_at"`
	BackendPercent         float64   `json:"backend_percent,omitempty"`
	BackendUpdatedAt       time.Time `json:"backend_updated_at,omitempty"`
	RemoteAhead            bool      `json:"remote_ahead,omitempty"`
	SuppressRemoteUntilPct float64   `json:"suppress_remote_until_percent,omitempty"`
	LastError              string    `json:"last_error,omitempty"`
}

type StateStore struct {
	mu      sync.Mutex
	path    string
	Entries map[string]StateEntry `json:"entries"`
}

func OpenState(path string) (*StateStore, error) {
	s := &StateStore{path: path, Entries: map[string]StateEntry{}}
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, s)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.Entries == nil {
		s.Entries = map[string]StateEntry{}
	}
	return s, nil
}

func (s *StateStore) Get(k string) (StateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Entries[k]
	return v, ok
}

func (s *StateStore) Put(k string, v StateEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Entries[k] = v
	return s.saveLocked()
}

func (s *StateStore) Snapshot() map[string]StateEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]StateEntry{}
	for k, v := range s.Entries {
		out[k] = v
	}
	return out
}

func (s *StateStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
