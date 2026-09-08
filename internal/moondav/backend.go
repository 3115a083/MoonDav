package moondav

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Backend interface {
	Push(bookID string, percent float64) error
	Pull(bookID string) (float64, time.Time, error)
}

type BackendError struct {
	Temporary bool
	Message   string
}

func (e *BackendError) Error() string { return e.Message }

func temporary(err error) bool {
	var be *BackendError
	return errors.As(err, &be) && be.Temporary
}

func classifyHTTP(prefix string, status int, body string) error {
	msg := fmt.Sprintf("%s: HTTP %d", prefix, status)
	if body != "" {
		msg += ": " + body
	}
	return &BackendError{
		Temporary: status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500,
		Message:   msg,
	}
}

func networkError(prefix string, err error) error {
	return &BackendError{Temporary: true, Message: prefix + ": " + err.Error()}
}

type noBackend struct{}

func (noBackend) Push(string, float64) error { return nil }
func (noBackend) Pull(string) (float64, time.Time, error) {
	return 0, time.Time{}, errors.New("backend disabled")
}

type CalibreWebBackend struct {
	base, token string
	client      *http.Client
}

func (b *CalibreWebBackend) stateURL(id string) string {
	return fmt.Sprintf("%s/kobo/%s/v1/library/%s/state", b.base, url.PathEscape(b.token), url.PathEscape(id))
}

func (b *CalibreWebBackend) Push(id string, percent float64) error {
	status := "Reading"
	if percent >= 99.5 {
		status = "Finished"
	}
	payload := map[string]any{"ReadingStates": []any{map[string]any{
		"CurrentBookmark": map[string]any{"ProgressPercent": percent, "ContentSourceProgressPercent": percent, "Location": nil},
		"Statistics":      nil,
		"StatusInfo":      map[string]any{"Status": status},
	}}}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, b.stateURL(id), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return networkError("calibre-web push", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		x, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return classifyHTTP("calibre-web push", resp.StatusCode, string(x))
	}
	return nil
}

func (b *CalibreWebBackend) Pull(id string) (float64, time.Time, error) {
	resp, err := b.client.Get(b.stateURL(id))
	if err != nil {
		return 0, time.Time{}, networkError("calibre-web pull", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, time.Time{}, classifyHTTP("calibre-web pull", resp.StatusCode, "")
	}
	var v []struct {
		LastModified    string `json:"LastModified"`
		CurrentBookmark struct {
			ProgressPercent float64 `json:"ProgressPercent"`
		} `json:"CurrentBookmark"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || len(v) == 0 {
		return 0, time.Time{}, &BackendError{Temporary: false, Message: "invalid calibre-web state"}
	}
	t, _ := time.Parse(time.RFC3339, v[0].LastModified)
	return v[0].CurrentBookmark.ProgressPercent, t, nil
}

type KOBackend struct {
	base, user, key string
	client          *http.Client
}

func (b *KOBackend) req(method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequest(method, b.base+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/vnd.koreader.v1+json")
	r.Header.Set("x-auth-user", b.user)
	r.Header.Set("x-auth-key", b.key)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, nil
}

func (b *KOBackend) Push(id string, p float64) error {
	payload := map[string]any{"document": id, "progress": fmt.Sprintf("%.2f%%", p), "percentage": p / 100, "device": "MoonDav", "device_id": "MOONDAV"}
	data, _ := json.Marshal(payload)
	req, _ := b.req(http.MethodPut, "/syncs/progress", bytes.NewReader(data))
	resp, err := b.client.Do(req)
	if err != nil {
		return networkError("booklore/kosync push", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return classifyHTTP("booklore/kosync push", resp.StatusCode, "")
	}
	return nil
}

func (b *KOBackend) Pull(id string) (float64, time.Time, error) {
	req, _ := b.req(http.MethodGet, "/syncs/progress/"+url.PathEscape(id), nil)
	resp, err := b.client.Do(req)
	if err != nil {
		return 0, time.Time{}, networkError("booklore/kosync pull", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, time.Time{}, classifyHTTP("booklore/kosync pull", resp.StatusCode, "")
	}
	var v struct {
		Percentage float64 `json:"percentage"`
		Timestamp  int64   `json:"timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, time.Time{}, &BackendError{Temporary: false, Message: "invalid booklore/kosync state"}
	}
	return v.Percentage * 100, time.Unix(v.Timestamp, 0), nil
}

func NewBackend(c Config) (Backend, error) {
	cli := &http.Client{Timeout: 10 * time.Second}
	switch strings.ToLower(c.BackendType) {
	case "", "none":
		return noBackend{}, nil
	case "calibre-web":
		if c.BackendURL == "" || c.BackendToken == "" {
			return nil, errors.New("calibre-web requires MOONDAV_BACKEND_URL and MOONDAV_BACKEND_TOKEN")
		}
		return &CalibreWebBackend{base: c.BackendURL, token: c.BackendToken, client: cli}, nil
	case "booklore", "kosync":
		key := c.BackendKey
		if key == "" && c.BackendPassword != "" {
			sum := md5.Sum([]byte(c.BackendPassword))
			key = fmt.Sprintf("%x", sum)
		}
		if c.BackendURL == "" || c.BackendUser == "" || key == "" {
			return nil, errors.New("booklore requires MOONDAV_BACKEND_URL, MOONDAV_BACKEND_USER and MOONDAV_BACKEND_PASSWORD or MOONDAV_BACKEND_KEY")
		}
		return &KOBackend{base: c.BackendURL, user: c.BackendUser, key: key, client: cli}, nil
	default:
		return nil, fmt.Errorf("unsupported MOONDAV_BACKEND %q", c.BackendType)
	}
}
