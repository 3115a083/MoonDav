package moondav

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	LibraryRoot     string
	ExactPositions  bool
	ShelfMode       string
	ShelfURL        string
	ShelfUser       string
	ShelfPassword   string
	ShelfRoot       string
	ShelfMaxFeedBytes int64

	NotifyAfter       time.Duration
	NotifyRepeat      time.Duration
	TelegramBotToken  string
	TelegramChatID    string
	WebhookURL        string
	WebhookBearer     string
	SMTPHost          string
	SMTPPort          int
	SMTPUser          string
	SMTPPassword      string
	SMTPFrom          string
	SMTPTo            string
	SMTPTLSMode       string
}

func LoadConfigFromEnv() (Config, error) {
	dataDir := env("MOONDAV_DATA_DIR", "/data")
	c := Config{
		Listen:          env("MOONDAV_LISTEN", ":8765"),
		DataDir:         dataDir,
		DAVUser:         secretEnv("MOONDAV_DAV_USER"),
		DAVPassword:     secretEnv("MOONDAV_DAV_PASSWORD"),
		AdminUser:       secretEnv("MOONDAV_ADMIN_USER"),
		AdminPassword:   secretEnv("MOONDAV_ADMIN_PASSWORD"),
		BasePath:        cleanBasePath(env("MOONDAV_BASE_PATH", "/dav/")),
		MaxUploadBytes:  envInt64("MOONDAV_MAX_UPLOAD_BYTES", 8<<20),
		ConflictPolicy:  strings.ToLower(env("MOONDAV_CONFLICT_POLICY", "furthest")),
		BackendType:     strings.ToLower(env("MOONDAV_BACKEND", "none")),
		BackendURL:      strings.TrimRight(env("MOONDAV_BACKEND_URL", ""), "/"),
		BackendToken:    secretEnv("MOONDAV_BACKEND_TOKEN"),
		BackendUser:     secretEnv("MOONDAV_BACKEND_USER"),
		BackendKey:      secretEnv("MOONDAV_BACKEND_KEY"),
		BackendPassword: secretEnv("MOONDAV_BACKEND_PASSWORD"),
		BookMapFile:     env("MOONDAV_BOOK_MAP_FILE", filepath.Join(dataDir, "book-map.json")),
		LibraryRoot:     env("MOONDAV_LIBRARY_ROOT", ""),
		ExactPositions:  envBool("MOONDAV_EXACT_POSITIONS", false),
		ShelfMode:       strings.ToLower(env("MOONDAV_SHELF_MODE", "off")),
		ShelfURL:        strings.TrimRight(env("MOONDAV_SHELF_URL", ""), "/"),
		ShelfUser:       secretEnv("MOONDAV_SHELF_USER"),
		ShelfPassword:   secretEnv("MOONDAV_SHELF_PASSWORD"),
		ShelfRoot:       env("MOONDAV_SHELF_ROOT", ""),
		ShelfMaxFeedBytes: envInt64("MOONDAV_SHELF_MAX_FEED_BYTES", 8<<20),

		NotifyAfter:      envDuration("MOONDAV_NOTIFY_AFTER", 10*time.Minute),
		NotifyRepeat:     envDuration("MOONDAV_NOTIFY_REPEAT", 6*time.Hour),
		TelegramBotToken: secretEnv("MOONDAV_TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   env("MOONDAV_TELEGRAM_CHAT_ID", ""),
		WebhookURL:       env("MOONDAV_WEBHOOK_URL", ""),
		WebhookBearer:    secretEnv("MOONDAV_WEBHOOK_BEARER"),
		SMTPHost:         env("MOONDAV_SMTP_HOST", ""),
		SMTPPort:         envInt("MOONDAV_SMTP_PORT", 587),
		SMTPUser:         secretEnv("MOONDAV_SMTP_USER"),
		SMTPPassword:     secretEnv("MOONDAV_SMTP_PASSWORD"),
		SMTPFrom:         env("MOONDAV_SMTP_FROM", ""),
		SMTPTo:           env("MOONDAV_SMTP_TO", ""),
		SMTPTLSMode:      strings.ToLower(env("MOONDAV_SMTP_TLS", "starttls")),
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
	if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" {
		return c, errors.New("MOONDAV_SMTP_TLS must be starttls or tls")
	}
	switch c.ShelfMode {
	case "off":
	case "opds":
		if c.ShelfURL == "" {
			return c, errors.New("MOONDAV_SHELF_URL is required when MOONDAV_SHELF_MODE=opds")
		}
	case "filesystem":
		if c.ShelfRoot == "" {
			return c, errors.New("MOONDAV_SHELF_ROOT is required when MOONDAV_SHELF_MODE=filesystem")
		}
	default:
		return c, errors.New("MOONDAV_SHELF_MODE must be off, opds, or filesystem")
	}
	if err := os.MkdirAll(filepath.Join(c.DataDir, "webdav"), 0700); err != nil {
		return c, err
	}
	return c, nil
}

func secretEnv(k string) string {
	if file := os.Getenv(k + "_FILE"); file != "" {
		if b, err := os.ReadFile(file); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return os.Getenv(k)
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseUint(v, 10, 16); err == nil && n <= 65535 {
			return int(n)
		}
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

func envBool(k string, d bool) bool {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return d
}

func envDuration(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if n, err := time.ParseDuration(v); err == nil {
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
