package games

// Testers and the codes that make them (docs/PUBLISHERS.md, "Testers"): the
// publisher hands out codes and takes testers off; a player joins by a code,
// reads what they test, and leaves.

import (
	"context"
	"crypto/rand"
	stderrors "errors"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

const (
	// MaxTesters is how many players may test one game.
	MaxTesters = 500
	// MaxInvites is how many live codes one game may have at once.
	MaxInvites = 20

	defaultInviteDays = 30
	maxInviteDays     = 90
	maxInviteUses     = 1000

	// A player gets twenty tries an hour at a code: eight symbols of a
	// 32-symbol alphabet are not found by guessing at that pace.
	joinTries  = 20
	joinWindow = time.Hour

	inviteAttempts = 8
)

// Limiter counts tries. identity's is the real one.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (ok bool, retryAfter int, err error)
}

// InviteRequest is a new code as the publisher asks for it. Nil is the
// default: no limit on uses, 30 days.
type InviteRequest struct {
	Label   string
	MaxUses *int
	Days    *int
}

// TesterRow is one tester as the publisher lists them: the row, and who the
// player is.
type TesterRow struct {
	Tester   model.Tester
	Nickname string
	Handle   string
	Avatar   string
}

// Joined is what joining by a code answers.
type Joined struct {
	Game    model.Game
	Testing bool
	Already bool
}

// PlayerTest is one game a player tests, as their list shows it.
type PlayerTest struct {
	Game     model.Game
	Testing  bool
	JoinedAt time.Time
}

var (
	errInviteNotFound = errors.NotFound("invite_not_found")
	errTestersFull    = errors.Conflict("testers_full")
)

// Testers is a game's codes and testers, the newest first.
func (s *Studio) Testers(ctx context.Context, publisherID, gameID string) ([]model.TesterInvite, []TesterRow, error) {
	if _, err := s.owned(ctx, publisherID, gameID); err != nil {
		return nil, nil, err
	}
	invites, err := Invites(ctx, s.db, gameID, false)
	if err != nil {
		return nil, nil, errors.Internal(err)
	}
	var rows []struct {
		model.Tester
		Nickname  string
		Handle    string
		AvatarKey string
	}
	err = s.db.WithContext(ctx).Raw(`SELECT t.game_id, t.player_id, t.code, t.joined_at,
			p.nickname, p.handle, p.avatar_key
		FROM game_testers t JOIN players p ON p.id = t.player_id
		WHERE t.game_id = ? ORDER BY t.joined_at DESC, t.player_id`, gameID).Scan(&rows).Error
	if err != nil {
		return nil, nil, errors.Internal(err)
	}
	testers := make([]TesterRow, 0, len(rows))
	for _, r := range rows {
		testers = append(testers, TesterRow{Tester: r.Tester, Nickname: r.Nickname, Handle: r.Handle, Avatar: r.AvatarKey})
	}
	return invites, testers, nil
}

// Invite makes a code for one of the publisher's games.
func (s *Studio) Invite(ctx context.Context, who Account, gameID string, in InviteRequest) (*model.TesterInvite, error) {
	if _, err := s.owned(ctx, who.ID, gameID); err != nil {
		return nil, err
	}
	bad := map[string]string{}
	label := strings.TrimSpace(in.Label)
	if model.TooLong(label, model.MaxInviteLabel) {
		bad["label"] = "too_long"
	}
	uses := 0
	if in.MaxUses != nil {
		uses = *in.MaxUses
		if uses < 1 || uses > maxInviteUses {
			bad["max_uses"] = "1_to_1000"
		}
	}
	days := defaultInviteDays
	if in.Days != nil {
		days = *in.Days
		if days < 1 || days > maxInviteDays {
			bad["days"] = "1_to_90"
		}
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	return s.newInvite(ctx, who.ID, gameID, label, uses, days)
}

// Revoke stops a code at once. Who joined with it stays.
func (s *Studio) Revoke(ctx context.Context, who Account, gameID, code string) error {
	if _, err := s.owned(ctx, who.ID, gameID); err != nil {
		return err
	}
	res := s.db.WithContext(ctx).Model(&model.TesterInvite{}).
		Where("code = ? AND game_id = ? AND revoked_at IS NULL", normalizeInvite(code), gameID).
		Update("revoked_at", data.Now())
	if res.Error != nil {
		return errors.Internal(res.Error)
	}
	if res.RowsAffected > 0 {
		s.reg.record(ctx, who.ID, model.ActionInviteRevoked, gameID, map[string]any{"code": normalizeInvite(code)})
	}
	return nil
}

// RemoveTester takes a player off a game's testers. A game never published
// was theirs only as a tester, so its tokens for them stop at once too.
func (s *Studio) RemoveTester(ctx context.Context, who Account, gameID, playerID string) error {
	g, err := s.owned(ctx, who.ID, gameID)
	if err != nil {
		return err
	}
	if err := s.dropTester(ctx, g, playerID); err != nil {
		return err
	}
	s.reg.record(ctx, who.ID, model.ActionTesterRemoved, gameID, map[string]any{"player": playerID})
	return nil
}

// JoinTest is a player typing a code: they test its game from now on, its
// open build in place of the live game.
func (s *Studio) JoinTest(ctx context.Context, playerID, typed string) (*Joined, error) {
	code := normalizeInvite(typed)
	if !s.validInvite(code) {
		return nil, errors.Invalid(map[string]string{"code": "must_be_8_symbols"})
	}
	if s.Limiter != nil {
		ok, retry, err := s.Limiter.Allow(ctx, "tests:join:"+playerID, joinTries, joinWindow)
		if err != nil {
			return nil, errors.Internal(err)
		}
		if !ok {
			return nil, errors.TooManyRequests(retry)
		}
	}

	var gameID string
	already := false
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		var inv model.TesterInvite
		err := data.Conn(ctx, s.db).Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&inv, "code = ?", code).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return errInviteNotFound
		}
		if err != nil {
			return err
		}
		g, err := gameIn(ctx, s.db, inv.GameID)
		if err != nil {
			return err
		}
		if g == nil || g.Listing == enum.Suspended {
			return errInviteNotFound
		}
		gameID = g.ID

		// A tester typing the code again is told so, and the code's count does
		// not move: whether it still works for others is not their business.
		tester, err := IsTester(ctx, s.db, g.ID, playerID)
		if err != nil {
			return err
		}
		if tester {
			already = true
			return nil
		}
		if !inv.Live(data.Now()) {
			return errInviteNotFound
		}
		var n int64
		if err := data.Conn(ctx, s.db).Model(&model.Tester{}).Where("game_id = ?", g.ID).Count(&n).Error; err != nil {
			return err
		}
		if n >= MaxTesters {
			return errTestersFull
		}
		if err := data.Conn(ctx, s.db).Create(&model.Tester{
			GameID: g.ID, PlayerID: playerID, Code: code, JoinedAt: data.Now(),
		}).Error; err != nil {
			return err
		}
		return data.Conn(ctx, s.db).Model(&model.TesterInvite{}).Where("code = ?", code).
			Update("uses", gorm.Expr("uses + 1")).Error
	})
	if err != nil {
		var e *errors.Error
		if stderrors.As(err, &e) {
			return nil, e
		}
		return nil, errors.Internal(err)
	}
	if !already {
		s.reg.record(ctx, playerID, model.ActionTesterJoined, gameID, map[string]any{"code": code})
	}

	out := &Joined{Already: already}
	seen, err := SeenBy(ctx, s.db, playerID, gameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if seen != nil {
		out.Game, out.Testing = seen.Game, seen.Testing()
	} else {
		// Nothing to play until the next build: the game's name, no version.
		g, err := anyGame(ctx, s.db, gameID)
		if err != nil || g == nil {
			return nil, errors.Internal(err)
		}
		out.Game = *g
		out.Game.Version = ""
	}
	return out, nil
}

// PlayerTests is every game playerID tests, the newest first. Suspended
// games are left out.
func (s *Studio) PlayerTests(ctx context.Context, playerID string) ([]PlayerTest, error) {
	var rows []struct {
		GameID   string
		JoinedAt time.Time
	}
	err := s.db.WithContext(ctx).Raw(`SELECT t.game_id, t.joined_at FROM game_testers t
		JOIN games g ON g.id = t.game_id
		WHERE t.player_id = ? AND g.listing <> ?
		ORDER BY t.joined_at DESC, t.game_id`, playerID, enum.Suspended).Scan(&rows).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]PlayerTest, 0, len(rows))
	for _, r := range rows {
		seen, err := SeenBy(ctx, s.db, playerID, r.GameID)
		if err != nil {
			return nil, errors.Internal(err)
		}
		row := PlayerTest{JoinedAt: r.JoinedAt}
		if seen != nil {
			row.Game, row.Testing = seen.Game, seen.Testing()
		} else {
			g, err := anyGame(ctx, s.db, r.GameID)
			if err != nil {
				return nil, errors.Internal(err)
			}
			if g == nil {
				continue
			}
			row.Game = *g
			row.Game.Version = ""
		}
		out = append(out, row)
	}
	return out, nil
}

// LeaveTest is a player no longer testing a game: the live game from now on.
func (s *Studio) LeaveTest(ctx context.Context, playerID, gameID string) error {
	g, err := anyGame(ctx, s.db, gameID)
	if err != nil {
		return errors.Internal(err)
	}
	if g == nil {
		return errNotFound
	}
	if err := s.dropTester(ctx, g, playerID); err != nil {
		return err
	}
	s.reg.record(ctx, playerID, model.ActionTesterLeft, gameID, nil)
	return nil
}

// dropTester deletes the row. A game never published reached the player only
// as a tester, so its live tokens for them stop with it.
func (s *Studio) dropTester(ctx context.Context, g *model.Game, playerID string) error {
	res := s.db.WithContext(ctx).Where("game_id = ? AND player_id = ?", g.ID, playerID).Delete(&model.Tester{})
	if res.Error != nil {
		return errors.Internal(res.Error)
	}
	if res.RowsAffected == 0 {
		return errNotFound
	}
	if g.Listing != enum.Published && s.reg.revoker != nil {
		if err := s.reg.revoker.Revoke(ctx, g.ID, playerID); err != nil {
			applog.From(ctx).Error("live tokens not revoked", "game", g.ID, "player", playerID, "err", err)
		}
	}
	return nil
}

// newInvite draws a code and keeps it. actor is the publisher, or the admin
// making a reviewer's code.
func (s *Studio) newInvite(ctx context.Context, actor, gameID, label string, uses, days int) (*model.TesterInvite, error) {
	now := data.Now()
	var live int64
	err := s.db.WithContext(ctx).Model(&model.TesterInvite{}).
		Where("game_id = ? AND revoked_at IS NULL AND expires_at > ?", gameID, now).Count(&live).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if live >= MaxInvites {
		return nil, errors.Invalid(map[string]string{"invites": "too_many"})
	}

	inv := &model.TesterInvite{
		GameID: gameID, Label: label, MaxUses: uses,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour), CreatedBy: actor, CreatedAt: now,
	}
	for range inviteAttempts {
		inv.Code = s.drawInvite()
		res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(inv)
		if res.Error != nil {
			return nil, errors.Internal(res.Error)
		}
		if res.RowsAffected == 1 {
			s.reg.record(ctx, actor, model.ActionInviteCreated, gameID, map[string]any{
				"code": inv.Code, "label": label, "max_uses": uses, "days": days,
			})
			return inv, nil
		}
	}
	return nil, errors.Internal(stderrors.New("games: no free invite code"))
}

// Invites is a game's codes, the newest first; live only, when asked.
func Invites(ctx context.Context, db *gorm.DB, gameID string, liveOnly bool) ([]model.TesterInvite, error) {
	q := data.Conn(ctx, db).Where("game_id = ?", gameID)
	if liveOnly {
		q = q.Where("revoked_at IS NULL AND expires_at > ? AND (max_uses = 0 OR uses < max_uses)", data.Now())
	}
	var out []model.TesterInvite
	err := q.Order("created_at DESC, code").Find(&out).Error
	return out, err
}

// drawInvite is eight symbols of the alphabet, each uniformly.
func (s *Studio) drawInvite() string {
	n := len(s.Alphabet)
	limit := 256 - 256%n
	out := make([]byte, 0, model.InviteCodeLength)
	buf := make([]byte, 16)
	for len(out) < model.InviteCodeLength {
		if _, err := rand.Read(buf); err != nil {
			panic("games: no randomness available: " + err.Error())
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, s.Alphabet[int(b)%n])
			if len(out) == model.InviteCodeLength {
				break
			}
		}
	}
	return string(out)
}

// normalizeInvite is what a player meant: case does not count, and neither
// do the spaces and dashes a code is read out with.
func normalizeInvite(typed string) string {
	if len(typed) > 64 {
		return ""
	}
	var b strings.Builder
	for _, r := range typed {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

func (s *Studio) validInvite(code string) bool {
	if len(code) != model.InviteCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(s.Alphabet, rune(code[i])) {
			return false
		}
	}
	return true
}
