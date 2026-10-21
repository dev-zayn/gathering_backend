package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

func TestReady(t *testing.T) {
	up := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("connection refused") }

	for _, tc := range []struct {
		name   string
		redis  Check
		status int
		body   string
	}{
		{"everything answers", up, http.StatusOK, `"status":"ok"`},
		{"redis does not", down, http.StatusServiceUnavailable, `"error":"not_ready"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, api := New("gathering", "test", applog.New("gathering"))
			ready(api, map[string]Check{"postgres": up, "redis": tc.redis})

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("got %d %s, want %d with %s", rec.Code, rec.Body, tc.status, tc.body)
			}
			// Anybody may ask, so the body must not say what is down.
			if strings.Contains(rec.Body.String(), "redis") {
				t.Fatalf("body names the failing check: %s", rec.Body)
			}
		})
	}
}
