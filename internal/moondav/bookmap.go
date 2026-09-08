package moondav

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type BookMapping struct {
	BackendID string `json:"backend_id"`
	EPUBPath  string `json:"epub_path,omitempty"`
}

func (m *BookMapping) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var legacy string
		if err := json.Unmarshal(b, &legacy); err != nil {
			return err
		}
		m.BackendID = legacy
		return nil
	}
	type alias BookMapping
	var v alias
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*m = BookMapping(v)
	return nil
}

type BookMap struct {
	mu      sync.RWMutex
	path    string
	Entries map[string]BookMapping `json:"entries"`
}

func OpenBookMap(path string) (*BookMap, error) {
	m := &BookMap{path: path, Entries: map[string]BookMapping{}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, m); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if m.Entries == nil {
		m.Entries = map[string]BookMapping{}
	}
	return m, nil
}

func (m *BookMap) Snapshot() map[string]BookMapping {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]BookMapping, len(m.Entries))
	for k, v := range m.Entries {
		out[k] = v
	}
	return out
}

func (m *BookMap) Resolve(key string) (string, bool) {
	v, ok := m.ResolveMapping(key)
	return v.BackendID, ok && v.BackendID != ""
}

func (m *BookMap) ResolveMapping(key string) (BookMapping, bool) {
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
	cur := m.Entries[key]
	cur.BackendID = value
	m.Entries[key] = cur
	return m.saveLocked()
}

func (m *BookMap) SetDetailed(key string, value BookMapping) error {
	key = strings.ToLower(strings.TrimSpace(key))
	value.BackendID = strings.TrimSpace(value.BackendID)
	value.EPUBPath = filepath.ToSlash(strings.TrimSpace(value.EPUBPath))
	if key == "" || value.BackendID == "" {
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
