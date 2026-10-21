package games

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperr "github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// The address is typed by an admin and fetched by us, from inside our own
// network. Which addresses those are is internal/pkg/outbound's to say, and is
// tested there; this is about the fetcher asking it at all.

// httptest listens on loopback, so a fetcher that guards the dial cannot reach
// it. The server stands in for anything only we can see.
func TestLoopbackIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"a.b"}`))
	}))
	defer srv.Close()

	_, _, err := NewHTTPFetcher(false).Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("a manifest on loopback was fetched")
	}
	// The code is what a caller sees; the reason is underneath it, and it
	// matters that the refusal came from the guard and not from a timeout.
	var cause *apperr.Error
	if !errors.As(err, &cause) || !strings.Contains(cause.Cause.Error(), "not a public address") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// The URL an admin types is checked by nobody after the redirect, which is
// why the check is on the dial and not on the URL.
func TestARedirectInwardsIsRefused(t *testing.T) {
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"secret.internal"}`))
	}))
	defer inside.Close()

	// This one is reached with the guard off, so the redirect is what the guard
	// has to stop rather than the first hop.
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inside.URL, http.StatusFound)
	}))
	defer away.Close()

	_, _, err := NewHTTPFetcher(false).Fetch(context.Background(), away.URL)
	if err == nil {
		t.Fatal("a redirect into loopback was followed")
	}
}

// A laptop's fixture manifests are served by another container, and every
// address in that network is private.
func TestALaptopReachesTheFixtures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"fixture.xo","version":"1.0.0"}`))
	}))
	defer srv.Close()

	m, raw, err := NewHTTPFetcher(true).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("the fixtures are unreachable with the guard off: %v", err)
	}
	if m.ID != "fixture.xo" {
		t.Errorf("parsed id is %q", m.ID)
	}
	if len(raw) == 0 {
		t.Error("the raw document came back empty")
	}
}

func TestABodyLargerThanTheCapIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pad":"` + strings.Repeat("x", MaxSize+16) + `"}`))
	}))
	defer srv.Close()

	if _, _, err := NewHTTPFetcher(true).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("a document over the cap was accepted")
	}
}

// A page where a document should be is the most common mistake a studio
// makes, and it has to read as one.
func TestAPageInsteadOfADocumentIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>our game</title>`))
	}))
	defer srv.Close()

	_, _, err := NewHTTPFetcher(true).Fetch(context.Background(), srv.URL)
	var cause *apperr.Error
	if !errors.As(err, &cause) || cause.Code != "invalid_fields" || cause.Fields["manifest"] != "not_json" {
		t.Fatalf("a page was read as a manifest: %v", err)
	}
}
