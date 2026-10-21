package biz

import (
	"context"
	"crypto/rand"
	stderrors "errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// SignupRequest is one call carrying the whole profile. The phone comes from the
// signup token, so nobody registers a number they did not just prove they hold.
type SignupRequest struct {
	Phone    string
	Nickname string
	// Avatar is a bundled avatar's id, or the key the handler stored an upload
	// under. Empty is the default picture.
	Avatar   string
	Language string
}

type Signup struct {
	Tx      *data.Tx
	Players *identity.PlayerStore
	Issuer  *identity.Issuer
	Audit   audit.Log
}

// Do makes a player and signs them in: the row and the session, or neither.
// The handle is made from the nickname: its latin letters and digits,
// lowercase, and four digits so two people called Ali can both be Ali.
func (s *Signup) Do(ctx context.Context, in SignupRequest) (*identity.Verified, error) {
	nickname := strings.TrimSpace(in.Nickname)
	bad := map[string]string{}
	switch n := utf8.RuneCountInString(nickname); {
	case n == 0:
		bad["nickname"] = "required"
	case n < 2:
		bad["nickname"] = "too_short"
	case n > model.MaxNickname:
		bad["nickname"] = "too_long"
	}
	language := enum.Language(in.Language)
	if in.Language == "" {
		language = enum.Arabic
	} else if !language.Valid() {
		bad["language"] = "unknown"
	}
	if model.TooLong(in.Avatar, model.MaxAvatar) {
		bad["avatar"] = "too_long"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	// The number was checked at verify, but a second registration could have
	// started in between. The unique index below is what actually decides.
	existing, err := s.Players.ByPhone(ctx, in.Phone)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if existing != nil {
		return nil, identity.ErrPhoneTaken
	}

	phone := in.Phone
	player := &model.Player{
		ID: ids.New(ids.Player), Phone: &phone, Nickname: nickname,
		AvatarKey: in.Avatar, Language: language,
	}

	// Four digits are ten thousand Alis, so a clash is rare and a retry is
	// cheap. Each try is a transaction of its own: a failed insert poisons the
	// one it is in, so the session is opened inside it and the whole thing is
	// started again on a clash.
	const tries = 5
	stem := handleStem(nickname)
	for i := 0; i < tries; i++ {
		player.Handle = stem + "_" + digits()

		var out *identity.Verified
		err := s.Tx.Do(ctx, func(ctx context.Context) error {
			if err := s.Players.Create(ctx, player); err != nil {
				return err
			}
			v, err := s.Issuer.Open(ctx, player, "")
			out = v
			return err
		})
		switch {
		case err == nil:
			s.record(ctx, player)
			return out, nil
		case identity.IsPhoneConflict(err):
			return nil, identity.ErrPhoneTaken
		case identity.IsHandleConflict(err):
			continue
		}
		var e *errors.Error
		if stderrors.As(err, &e) {
			return nil, e
		}
		return nil, errors.Internal(err)
	}
	return nil, errors.Internal(fmt.Errorf("signup: no free handle for %q in %d tries", stem, tries))
}

// handleStem is the nickname's latin letters and digits, lowercase, or
// "player" when there are none: an Arabic name is not transliterated, it is
// shown as the nickname. Cut to leave room for the four digits.
func handleStem(nickname string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(nickname) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	stem := b.String()
	if stem == "" {
		stem = "player"
	}
	if max := model.MaxHandle - 5; len(stem) > max {
		stem = stem[:max]
	}
	return stem
}

// digits is the four random digits after the underscore. A variable so a test
// can make them collide.
var digits = func() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		panic("signup: no randomness available: " + err.Error())
	}
	return fmt.Sprintf("%04d", n)
}

// record logs a failed write rather than failing the sign-up it describes.
func (s *Signup) record(ctx context.Context, p *model.Player) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Write(ctx, model.Entry{
		Actor: p.ID, Action: model.ActionSignedUp, Subject: p.ID, Detail: map[string]any{"handle": p.Handle},
	}); err != nil {
		applog.From(ctx).Error("audit not written", "action", model.ActionSignedUp, "err", err)
	}
}
