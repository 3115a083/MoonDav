package moondav

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type BookMap struct {
	mu      sync.RWMutex
	path    string
	Entries map[string]string `json:"entries"`
}

func OpenBookMap(path string) (*BookMap, error) {
	m := &BookMap{path: path, Entries: map[string]string{}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, m); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if m.Entries == nil {
		m.Entries = map[string]string{}
	}
	return m, nil
}

func (m *BookMap) Snapshot() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.Entries))
	for k, v := range m.Entries {
		out[k] = v
	}
	return out
}

func (m *BookMap) Resolve(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.Entries[strings.ToLower(strings.TrimSpace(key))]
	return v, ok
}

func (m *BookMap) Set(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return os.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries[key] = value
	return m.saveLocked()
}

func (m *BookMap) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Entries, strings.ToLower(strings.TrimSpace(key)))
	return m.saveLocked()
}

func (m *BookMap) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
