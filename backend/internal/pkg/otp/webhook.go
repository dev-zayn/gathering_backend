// Package otp carries a code to a phone. The sender is a webhook, so any SMS or
// WhatsApp gateway is a few lines of glue away; a laptop with none sends the
// code to the log instead.
package otp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Webhook posts every message to one URL as JSON and takes any 2xx as accepted.
// What the gateway does with it, and over which channel, is the gateway's.
type Webhook struct {
	url   string
	token string
	http  *http.Client
}

// NewWebhook sends to url, with token as a bearer when there is one. The
// timeout bounds the whole exchange: a gateway that hangs must not hold the
// sign-in with it.
func NewWebhook(url, token string, timeout time.Duration) *Webhook {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Webhook{url: url, token: token, http: &http.Client{Timeout: timeout}}
}

// Send returns nil once the gateway has accepted the code, which is not
// delivery.
func (w *Webhook) Send(ctx context.Context, e164, code string) error {
	return w.post(ctx, map[string]string{"phone": e164, "code": code, "kind": "otp"})
}

// Notify carries anything else we have to tell a player. Same hook, no code.
func (w *Webhook) Notify(ctx context.Context, e164, text string) error {
	return w.post(ctx, map[string]string{"phone": e164, "text": text, "kind": "notice"})
}

func (w *Webhook) post(ctx context.Context, payload map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("otp: webhook: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}

	res, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("otp: webhook: %w", err)
	}
	defer res.Body.Close()

	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("otp: webhook returned %d: %s", res.StatusCode, trim(string(b)))
	}
	return nil
}

// trim keeps a gateway failure readable in a log line without pasting a page
// of HTML into it.
func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
