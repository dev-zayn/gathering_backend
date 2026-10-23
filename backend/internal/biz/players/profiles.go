package players

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jamaa/gathering/backend/internal/biz/audit"
	"github.com/jamaa/gathering/backend/internal/biz/identity"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// Profiles is the record as its owner edits it and as others read it.
type Profiles struct {
	Players *identity.PlayerStore
	Audit   audit.Log

	// HandleCooldown is how long a changed @name is kept before it may change
	// again: friends search by it, and a name that moves every day is a name
	// nobody finds.
	HandleCooldown time.Duration
}

// Update is what PATCH /v1/me may carry. A nil field is left alone.
type Update struct {
	Nickname *string
	Handle   *string
	Bio      *string
	Avatar   *string
	Language *string
}

var (
	handleRe = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)
	avatarRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

// Limits on what a profile holds. The nickname is what sits under an avatar in
// a seat, so it is short.
const (
	MinNickname = 2
	MaxNickname = 20
	MaxBio      = 120
)

// Me is the signed-in player's own record. A token whose player is gone is a
// token that opens nothing.
func (p *Profiles) Me(ctx context.Context, id string) (*model.Player, error) {
	pl, err := p.Players.ByID(ctx, id)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if pl == nil {
		return nil, errors.Unauthorized("invalid_token")
	}
	return pl, nil
}

// Edit applies an Update, every problem reported at once.
func (p *Profiles) Edit(ctx context.Context, id string, in Update) (*model.Player, error) {
	pl, err := p.Me(ctx, id)
	if err != nil {
		return nil, err
	}

	bad := map[string]string{}
	changed := map[string]any{}
	now := time.Now()

	if in.Nickname != nil {
		n := strings.TrimSpace(*in.Nickname)
		if c := utf8.RuneCountInString(n); c < MinNickname || c > MaxNickname {
			bad["nickname"] = "2_to_20"
		} else {
			pl.Nickname = n
			changed["nickname"] = n
		}
	}
	if in.Bio != nil {
		b := strings.TrimSpace(*in.Bio)
		if utf8.RuneCountInString(b) > MaxBio {
			bad["bio"] = "too_long"
		} else {
			pl.Bio = b
		}
	}
	if in.Avatar != nil {
		if !avatarRe.MatchString(*in.Avatar) {
			bad["avatar"] = "unknown"
		} else {
			// A bundled avatar: an id the app ships. Uploaded pictures carry a
			// key with a slash and are set by the upload, never here.
			pl.AvatarKey = *in.Avatar
		}
	}
	if in.Language != nil {
		l := enum.Language(*in.Language)
		if !l.Valid() {
			bad["language"] = "unknown"
		} else {
			pl.Language = l
		}
	}
	if in.Handle != nil {
		h := strings.ToLower(strings.TrimSpace(*in.Handle))
		switch {
		case !handleRe.MatchString(h):
			bad["handle"] = "letters_digits_underscore_3_to_20"
		case h == pl.Handle:
			// The same name: nothing to do, and no cooldown spent.
		case pl.HandleChangedAt != nil && now.Sub(*pl.HandleChangedAt) < p.HandleCooldown:
			return nil, errors.Conflict("handle_cooldown")
		default:
			taken, err := p.Players.HandleTaken(ctx, h)
			if err != nil {
				return nil, errors.Internal(err)
			}
			if taken {
				return nil, errors.Conflict("handle_taken")
			}
			pl.Handle = h
			pl.HandleChangedAt = &now
			changed["handle"] = h
		}
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	if err := p.Players.Update(ctx, pl); err != nil {
		return nil, errors.Internal(err)
	}
	if len(changed) > 0 {
		p.record(ctx, pl.ID, model.ActionProfileEdited, pl.ID, changed)
	}
	return pl, nil
}

// Search finds players by the start of their handle. Three characters at
// least: two would list half the platform.
func (p *Profiles) Search(ctx context.Context, q string, limit int) ([]model.Player, error) {
	q = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(q, "@")))
	if utf8.RuneCountInString(q) < 3 {
		return nil, errors.Invalid(map[string]string{"q": "min_3"})
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	out, err := p.Players.Search(ctx, q, limit)
	if err != nil {
		return nil, errors.Internal(err)
	}
	return out, nil
}

// record writes the audit line and never fails the request over it.
func (p *Profiles) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if p.Audit == nil {
		return
	}
	err := p.Audit.Write(ctx, model.Entry{
		At: time.Now(), Actor: actor, Action: action, Subject: subject, Detail: detail,
	})
	if err != nil {
		applog.From(ctx).Warn("audit write failed", "action", action, "err", err)
	}
}
