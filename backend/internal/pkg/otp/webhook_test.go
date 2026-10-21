package otp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateway is a fake on the other end of the hook, remembering what arrived.
type gateway struct {
	mu     sync.Mutex
	status int
	body   string

	auth, contentType string
	payload           map[string]string
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	g.auth, g.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
	g.payload = map[string]string{}
	_ = json.Unmarshal(b, &g.payload)
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(g.status)
	_, _ = io.WriteString(w, g.body)
}

func newGateway(t *testing.T, status int) (*gateway, string) {
	t.Helper()
	g := &gateway{status: status}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv.URL
}

func TestSendPostsTheCodeAsJSONWithTheBearer(t *testing.T) {
	g, url := newGateway(t, http.StatusOK)
	hook := NewWebhook(url, "secret-1", time.Second)

	if err := hook.Send(t.Context(), "+9647701234567", "123456"); err != nil {
		t.Fatal(err)
	}
	if g.auth != "Bearer secret-1" || g.contentType != "application/json" {
		t.Errorf("headers were %q, %q", g.auth, g.contentType)
	}
	want := map[string]string{"phone": "+9647701234567", "code": "123456", "kind": "otp"}
	if len(g.payload) != len(want) || g.payload["phone"] != want["phone"] || g.payload["code"] != want["code"] || g.payload["kind"] != want["kind"] {
		t.Errorf("payload was %v, want %v", g.payload, want)
	}
}

func TestNotifyCarriesTextInstead(t *testing.T) {
	g, url := newGateway(t, http.StatusAccepted)
	hook := NewWebhook(url, "", time.Second)

	if err := hook.Notify(t.Context(), "+9647701234567", "تمت إضافتك إلى الغرفة"); err != nil {
		t.Fatal(err)
	}
	if g.auth != "" {
		t.Errorf("no token was configured and Authorization was %q", g.auth)
	}
	want := map[string]string{"phone": "+9647701234567", "text": "تمت إضافتك إلى الغرفة", "kind": "notice"}
	if len(g.payload) != len(want) || g.payload["text"] != want["text"] || g.payload["kind"] != want["kind"] {
		t.Errorf("payload was %v, want %v", g.payload, want)
	}
}

// Anything outside 2xx is a failure, with the gateway's words kept short.
func TestAnythingButSuccessIsAnError(t *testing.T) {
	g, url := newGateway(t, http.StatusBadGateway)
	g.body = "<html>" + strings.Repeat("x", 1000) + "</html>"
	hook := NewWebhook(url, "", time.Second)

	err := hook.Send(t.Context(), "+9647701234567", "123456")
	if err == nil {
		t.Fatal("a 502 was taken as sent")
	}
	if !strings.Contains(err.Error(), "502") || len(err.Error()) > 400 {
		t.Errorf("error was %q", err)
	}
	if strings.Contains(err.Error(), "123456") {
		t.Errorf("the code leaked into the error: %q", err)
	}

	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent} {
		g.status = status
		if err := hook.Send(t.Context(), "+9647701234567", "123456"); err != nil {
			t.Errorf("%d was refused: %v", status, err)
		}
	}
}

// A gateway that hangs is let go of at the timeout, and one that is gone at
// once.
func TestAGatewayThatHangsOrIsGoneFails(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)

	start := time.Now()
	if err := NewWebhook(slow.URL, "", 50*time.Millisecond).Send(t.Context(), "+9647701234567", "1"); err == nil {
		t.Fatal("a hanging gateway was taken as sent")
	}
	if time.Since(start) > time.Second {
		t.Errorf("the timeout was not kept: %s", time.Since(start))
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if err := NewWebhook(gone.URL, "", time.Second).Send(t.Context(), "+9647701234567", "1"); err == nil {
		t.Fatal("a closed gateway was taken as sent")
	}
}
