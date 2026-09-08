package moondav

import (
	"encoding/json"
	"os"
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
	v, ok := m.Entries[strings.ToLower(key)]
	return v, ok
}
