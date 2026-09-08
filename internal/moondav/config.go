package moondav

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Listen          string
	DataDir         string
	DAVUser         string
	DAVPassword     string
	AdminUser       string
	AdminPassword   string
	BasePath        string
	MaxUploadBytes  int64
	ConflictPolicy  string
	BackendType     string
	BackendURL      string
	BackendToken    string
	BackendUser     string
	BackendKey      string
	BackendPassword string
	BookMapFile     string
}

func LoadConfigFromEnv() (Config, error) {
	c := Config{
		Listen:          env("MOONDAV_LISTEN", ":8765"),
		DataDir:         env("MOONDAV_DATA_DIR", "/data"),
		DAVUser:         os.Getenv("MOONDAV_DAV_USER"),
		DAVPassword:     os.Getenv("MOONDAV_DAV_PASSWORD"),
		AdminUser:       os.Getenv("MOONDAV_ADMIN_USER"),
		AdminPassword:   os.Getenv("MOONDAV_ADMIN_PASSWORD"),
		BasePath:        cleanBasePath(env("MOONDAV_BASE_PATH", "/dav/")),
		MaxUploadBytes:  envInt64("MOONDAV_MAX_UPLOAD_BYTES", 8<<20),
		ConflictPolicy:  strings.ToLower(env("MOONDAV_CONFLICT_POLICY", "furthest")),
		BackendType:     strings.ToLower(env("MOONDAV_BACKEND", "none")),
		BackendURL:      strings.TrimRight(os.Getenv("MOONDAV_BACKEND_URL"), "/"),
		BackendToken:    os.Getenv("MOONDAV_BACKEND_TOKEN"),
		BackendUser:     os.Getenv("MOONDAV_BACKEND_USER"),
		BackendKey:      os.Getenv("MOONDAV_BACKEND_KEY"),
		BackendPassword: os.Getenv("MOONDAV_BACKEND_PASSWORD"),
		BookMapFile:     env("MOONDAV_BOOK_MAP_FILE", filepath.Join(env("MOONDAV_DATA_DIR", "/data"), "book-map.json")),
	}
	if c.DAVUser == "" || c.DAVPassword == "" {
		return c, errors.New("MOONDAV_DAV_USER and MOONDAV_DAV_PASSWORD are required")
	}
	if c.AdminUser == "" || c.AdminPassword == "" {
		return c, errors.New("MOONDAV_ADMIN_USER and MOONDAV_ADMIN_PASSWORD are required")
	}
	if c.ConflictPolicy != "furthest" && c.ConflictPolicy != "latest" {
		return c, errors.New("MOONDAV_CONFLICT_POLICY must be furthest or latest")
	}
	if err := os.MkdirAll(filepath.Join(c.DataDir, "webdav"), 0700); err != nil {
		return c, err
	}
	return c, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt64(k string, d int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return d
}

func cleanBasePath(v string) string {
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v
}
