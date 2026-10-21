package biztest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/model"
)

// Stand-ins for systems that are not ours, defined structurally so this package
// does not import the packages whose tests use it.

// Files is object storage, recording what was written and what was taken back:
// removing the object when the row fails is a rule, not a detail.
type Files struct {
	mu      sync.Mutex
	put     map[string][]byte
	types   map[string]string
	Deleted []string

	PutErr error
	URLErr error

	// Signed records every key a link was minted for and for how long: a
	// picture behind a link that never expires is what this catches.
	Signed map[string]time.Duration
}

func NewFiles() *Files {
	return &Files{put: map[string][]byte{}, types: map[string]string{}, Signed: map[string]time.Duration{}}
}

func (f *Files) Put(_ context.Context, key, contentType string, data []byte) error {
	if f.PutErr != nil {
		return f.PutErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.put[key] = append([]byte(nil), data...)
	f.types[key] = contentType
	return nil
}

func (f *Files) URL(_ context.Context, key string, ttl time.Duration) (string, error) {
	if f.URLErr != nil {
		return "", f.URLErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Signed[key] = ttl
	return "https://signed.test/" + key, nil
}

func (f *Files) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deleted = append(f.Deleted, key)
	delete(f.put, key)
	return nil
}

func (f *Files) Stored() map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range f.put {
		out[k] = v
	}
	return out
}

// TypeOf is the content type a key was put with, or "" when it never was.
func (f *Files) TypeOf(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.types[key]
}

// Fetcher is a studio's server: the documents it publishes, by address, and
// the ones it fails on. Hits is every address asked, in order, which is what
// a capped round is checked by.
type Fetcher struct {
	mu   sync.Mutex
	Docs map[string]*model.Manifest
	Errs map[string]error
	Hits []string
}

func NewFetcher() *Fetcher {
	return &Fetcher{Docs: map[string]*model.Manifest{}, Errs: map[string]error{}}
}

func (f *Fetcher) Fetch(_ context.Context, url string) (*model.Manifest, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Hits = append(f.Hits, url)
	if err := f.Errs[url]; err != nil {
		return nil, nil, err
	}
	m, ok := f.Docs[url]
	if !ok {
		return nil, nil, errors.New("no such document")
	}
	// The bytes are the document: a held one is parsed again from them. A
	// copy goes out, so Check filling in a default cannot edit the fixture.
	raw, _ := json.Marshal(m)
	var copied model.Manifest
	_ = json.Unmarshal(raw, &copied)
	return &copied, raw, nil
}

// Audit records who did what, and reads it back: the stand-in for audit.Log.
type Audit struct {
	mu      sync.Mutex
	entries []model.Entry
	Err     error
}

func (f *Audit) Write(_ context.Context, e model.Entry) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return nil
}

func (f *Audit) List(context.Context, model.Query) ([]model.Entry, error) {
	return f.Written(), nil
}

func (f *Audit) Written() []model.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Entry(nil), f.entries...)
}

// Whitelist is the revocation list. What Valid answers, and what it was asked:
// every test wants the default — listed, no error — so the zero value is that.
type Whitelist struct {
	mu           sync.Mutex
	AllowedGames []string
	// Revoked is every session cleared, in order; RevokedGames every game
	// (or game and player, joined by a colon) cleared.
	Revoked      []string
	RevokedGames []string

	Invalid  bool
	ValidErr error
	Asked    []string
}

func (f *Whitelist) Allow(context.Context, string, string, time.Duration) error { return nil }

func (f *Whitelist) Valid(_ context.Context, jti string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Asked = append(f.Asked, jti)
	return !f.Invalid, f.ValidErr
}

func (f *Whitelist) Touch(_ context.Context, jti string, _ time.Duration) (bool, error) {
	return f.Valid(context.Background(), jti)
}

func (f *Whitelist) Revoke(context.Context, string) error { return nil }

func (f *Whitelist) RevokeSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Revoked = append(f.Revoked, sessionID)
	return nil
}

func (f *Whitelist) AllowGame(_ context.Context, jti, _, _ string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AllowedGames = append(f.AllowedGames, jti)
	return nil
}

func (f *Whitelist) RevokeGame(_ context.Context, gameID, playerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := gameID
	if playerID != "" {
		key += ":" + playerID
	}
	f.RevokedGames = append(f.RevokedGames, key)
	return nil
}

// Sender is the OTP vendor: which numbers were messaged, and the code each
// last got, so a test can type it.
type Sender struct {
	mu    sync.Mutex
	codes map[string]string
	Sent  []string
	Err   error
}

func (s *Sender) Send(_ context.Context, phone, code string) error {
	if s.Err != nil {
		return s.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codes == nil {
		s.codes = map[string]string{}
	}
	s.codes[phone] = code
	s.Sent = append(s.Sent, phone)
	return nil
}

// Notify counts like Send: a notice is a message too.
func (s *Sender) Notify(_ context.Context, phone, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sent = append(s.Sent, phone)
	return nil
}

// Code is what the number was last sent, or "" if nothing was.
func (s *Sender) Code(phone string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codes[phone]
}
