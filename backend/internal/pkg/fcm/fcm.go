// Package fcm sends one message to one phone through Firebase Cloud
// Messaging's HTTP v1 API, signed in as a service account: a JWT signed with
// the account's key is traded at the key's token_uri for an access token,
// which is kept until it is about to expire. It knows nothing of what a
// message says; biz/push writes them. docs/PUSH.md is the contract.
package fcm

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// DefaultEndpoint is Firebase's own address. A test points Endpoint at a
// fake instead.
const DefaultEndpoint = "https://fcm.googleapis.com"

// Scope is the one permission a send needs.
const Scope = "https://www.googleapis.com/auth/firebase.messaging"

// googleTokenURI is where a key that names no token_uri trades its JWT.
const googleTokenURI = "https://oauth2.googleapis.com/token"

// Config is one Firebase project, and the key to send to it with.
type Config struct {
	ProjectID string

	// Credentials is the service account's JSON key, as the Firebase
	// console hands it out. Read once, never logged.
	Credentials []byte

	// Endpoint is the API's base URL; empty is DefaultEndpoint.
	Endpoint string

	// Timeout bounds one call, and one token exchange; zero is ten seconds.
	Timeout time.Duration
}

// Client sends to one project. It is safe for concurrent use.
type Client struct {
	url  string
	http *http.Client
}

// New checks the key and the endpoint and answers a client that has not yet
// called anybody: the first send trades the key for a token.
func New(cfg Config) (*Client, error) {
	project := strings.TrimSpace(cfg.ProjectID)
	if project == "" {
		return nil, errors.New("fcm: no project id")
	}
	key, err := readKey(cfg.Credentials)
	if err != nil {
		return nil, err
	}
	base, err := endpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// The token source keeps this context for every exchange it makes, so it
	// is the client's and not any one call's; the client in it bounds each
	// exchange.
	exchange := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: timeout})
	signer := &jwt.Config{
		Email:        key.ClientEmail,
		PrivateKey:   []byte(key.PrivateKey),
		PrivateKeyID: key.PrivateKeyID,
		Scopes:       []string{Scope},
		TokenURL:     key.TokenURI,
	}
	return &Client{
		url: base + "/v1/projects/" + url.PathEscape(project) + "/messages:send",
		http: &http.Client{
			Timeout:   timeout,
			Transport: &oauth2.Transport{Source: signer.TokenSource(exchange), Base: http.DefaultTransport},
		},
	}, nil
}

// Send posts one message. Nil is Firebase taking it, which is not delivery.
// A refusal is an *Error, which says whether the token is gone for good
// (Gone) or the same message may go through later (Temporary); anything else
// (the token exchange, the network) is a plain error. Neither names the
// message's token.
func (c *Client) Send(ctx context.Context, m Message) error {
	body, err := json.Marshal(struct {
		Message Message `json:"message"`
	}{m})
	if err != nil {
		return fmt.Errorf("fcm: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fcm: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}
	return refusal(res.StatusCode, raw, m.Target())
}

// serviceAccount is the part of a service account's JSON key a send needs.
type serviceAccount struct {
	Type         string `json:"type"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// readKey is the key, checked now so a bad one stops the api at boot rather
// than failing every push. No error quotes the key.
func readKey(raw []byte) (*serviceAccount, error) {
	var k serviceAccount
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("fcm: the credentials are not a JSON key: %w", err)
	}
	if k.Type != "service_account" {
		return nil, fmt.Errorf("fcm: the credentials are not a service account's key (type %q)", k.Type)
	}
	if k.ClientEmail == "" {
		return nil, errors.New("fcm: the key has no client_email")
	}
	if !rsaKey(k.PrivateKey) {
		return nil, errors.New("fcm: the key's private_key is not an RSA key in PEM")
	}
	if k.TokenURI == "" {
		k.TokenURI = googleTokenURI
	}
	if !address(k.TokenURI) {
		return nil, errors.New("fcm: the key's token_uri is not an http(s) address")
	}
	return &k, nil
}

// rsaKey says whether s is an RSA private key in PEM, PKCS #8 as Google
// writes them or PKCS #1.
func rsaKey(s string) bool {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return false
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		_, ok := k.(*rsa.PrivateKey)
		return ok
	}
	_, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	return err == nil
}

// endpoint is the base URL to send to, without a trailing slash.
func endpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultEndpoint
	}
	if !address(raw) {
		return "", fmt.Errorf("fcm: the endpoint %q is not an http(s) address", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

func address(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
