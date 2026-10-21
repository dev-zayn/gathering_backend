package guests

import (
	"sync"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/identity"
)

// fakeMinter records what a game token was minted with: the claims are the
// whole of what a launch decides, so they are what the tests read.
type fakeMinter struct {
	mu    sync.Mutex
	games []identity.GameClaims
}

func (f *fakeMinter) MintOTP(string, time.Duration) (string, error)    { return "otp", nil }
func (f *fakeMinter) MintSignup(string, time.Duration) (string, error) { return "signup", nil }
func (f *fakeMinter) MintSession(string, string, bool, time.Duration) (string, string, error) {
	return "session", "jti", nil
}
func (f *fakeMinter) MintAdmin(string, []string, time.Duration) (string, string, error) {
	return "admin", "jti-admin", nil
}
func (f *fakeMinter) MintRoom(identity.RoomClaims, time.Duration) (string, string, error) {
	return "room", "jti-room", nil
}
func (f *fakeMinter) MintGame(in identity.GameClaims, _ time.Duration) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.games = append(f.games, in)
	return "game-token", "jti-game", nil
}

// minted is what was minted so far, copied out from under the lock.
func (f *fakeMinter) minted() []identity.GameClaims {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]identity.GameClaims(nil), f.games...)
}
