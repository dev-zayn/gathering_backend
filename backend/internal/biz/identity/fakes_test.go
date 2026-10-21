package identity

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

type fakeWhitelist struct {
	mu           sync.Mutex
	allowedGames []string
	revoked      []string

	// What Valid answers, and what it was asked. Every other test wants the
	// default — listed, no error — so the zero value is that.
	invalid  bool
	validErr error
	asked    []string
}

func (f *fakeWhitelist) Allow(context.Context, string, string, time.Duration) error { return nil }

func (f *fakeWhitelist) Valid(_ context.Context, jti string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, jti)
	return !f.invalid, f.validErr
}
func (f *fakeWhitelist) Touch(_ context.Context, jti string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, jti)
	return !f.invalid, f.validErr
}

func (f *fakeWhitelist) Revoke(_ context.Context, jti string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, jti)
	return nil
}
func (f *fakeWhitelist) RevokeSession(context.Context, string) error { return nil }
func (f *fakeWhitelist) AllowGame(_ context.Context, jti, _, _ string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowedGames = append(f.allowedGames, jti)
	return nil
}
func (f *fakeWhitelist) RevokeGame(context.Context, string, string) error { return nil }

// fakeAdmins gives every subject the admin id adm_<subject>, and keeps the
// name it was last told.
type fakeAdmins struct {
	mu   sync.Mutex
	rows map[string]*model.Admin
}

func (f *fakeAdmins) Enroll(_ context.Context, subject, name, email string) (*model.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "adm_" + subject
	if f.rows == nil {
		f.rows = map[string]*model.Admin{}
	}
	if a, ok := f.rows[id]; ok {
		out := *a
		return &out, nil
	}
	f.rows[id] = &model.Admin{ID: id, Subject: subject, Name: name, Email: email, CreatedAt: data.Now()}
	out := *f.rows[id]
	return &out, nil
}

func (f *fakeAdmins) Seen(_ context.Context, id, name, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.rows[id]; a != nil {
		if name != "" {
			a.Name = name
		}
		if email != "" {
			a.Email = email
		}
		now := data.Now()
		a.SeenAt = &now
	}
	return nil
}

func (f *fakeAdmins) ByID(_ context.Context, id string) (*model.Admin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.rows[id]; a != nil {
		out := *a
		return &out, nil
	}
	return nil, nil
}

// fakeAudit keeps what was written.
type fakeAudit struct {
	audit.Log
	written []model.Entry
}

func (f *fakeAudit) Write(_ context.Context, e model.Entry) error {
	f.written = append(f.written, e)
	return nil
}

// memOTP is the code store in memory: one pending code per number, and the
// counter beside it, the way Redis keeps them.
type memOTP struct {
	mu    sync.Mutex
	codes map[string]*Pending
}

func (m *memOTP) Put(_ context.Context, phone, hash string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]*Pending{}
	}
	m.codes[phone] = &Pending{Hash: hash}
	return nil
}

func (m *memOTP) Replace(_ context.Context, phone, hash string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.codes[phone]; p != nil {
		p.Hash = hash
	}
	return nil
}

func (m *memOTP) Get(_ context.Context, phone string) (Pending, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.codes[phone]; p != nil {
		return *p, true, nil
	}
	return Pending{}, false, nil
}

func (m *memOTP) Attempt(_ context.Context, phone string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.codes[phone]; p != nil {
		p.Attempts++
		return p.Attempts, nil
	}
	return 0, nil
}

func (m *memOTP) Clear(_ context.Context, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.codes, phone)
	return nil
}

// fakeSSO is the identity provider: a token is a name it knows, an error it
// was told to give, or nothing of its own.
type fakeSSO struct {
	ids  map[string]*Person
	errs map[string]error
	// down is what every call answers while the provider is unreachable.
	down error
}

func (f *fakeSSO) Verify(_ context.Context, raw string) (*Person, error) {
	if f.down != nil {
		return nil, f.down
	}
	if err := f.errs[raw]; err != nil {
		return nil, err
	}
	id, ok := f.ids[raw]
	if !ok {
		return nil, fmt.Errorf("%w: not one of ours", ErrSSOInvalid)
	}
	out := *id
	out.Roles = append([]string(nil), id.Roles...)
	return &out, nil
}
