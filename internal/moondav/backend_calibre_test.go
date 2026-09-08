package moondav

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalibreWebExactLocationRoundTrip(t *testing.T) {
	var received struct {
		ReadingStates []struct {
			CurrentBookmark struct {
				ProgressPercent float64          `json:"ProgressPercent"`
				Location        *ReadingLocation `json:"Location"`
			} `json:"CurrentBookmark"`
		} `json:"ReadingStates"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Fatalf("decode PUT: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"UpdateResults":[]}`))
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{
				"LastModified":"2026-09-08T12:00:00Z",
				"CurrentBookmark":{
					"ProgressPercent":42.5,
					"Location":{"Source":"text/ch1.xhtml","Type":"KoboSpan","Value":"kobo.3.1"}
				}
			}]`))
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	b := &CalibreWebBackend{base: srv.URL, token: "token", client: srv.Client()}
	loc := &ReadingLocation{Source: "text/ch1.xhtml", Type: "KoboSpan", Value: "kobo.3.1"}
	if err := b.PushState("book-uuid", 42.5, loc); err != nil {
		t.Fatal(err)
	}
	if len(received.ReadingStates) != 1 || received.ReadingStates[0].CurrentBookmark.Location == nil {
		t.Fatalf("location missing from calibre-web payload: %+v", received)
	}
	gotLoc := received.ReadingStates[0].CurrentBookmark.Location
	if *gotLoc != *loc {
		t.Fatalf("location mismatch: got %+v want %+v", gotLoc, loc)
	}

	state, err := b.PullState("book-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if state.Percent != 42.5 || state.Location == nil || *state.Location != *loc {
		t.Fatalf("unexpected pulled state: %+v", state)
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-09-08T12:00:00Z")
	if !state.UpdatedAt.Equal(wantTime) {
		t.Fatalf("unexpected timestamp: %v", state.UpdatedAt)
	}
}
