package tournaments

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

// The defaults a request may leave out: a knockout of eight, a league of
// five rounds at the whole table.
const (
	defaultSize   = 8
	defaultRounds = 5
)

// CreateRequest is a tournament as the app (or the console) asks for one.
// A zero number is the default; StartsAt is the console's.
type CreateRequest struct {
	Game       string
	Name       string
	Format     string
	BestOf     int
	Rounds     int
	Size       int
	Visibility string
	StartsAt   *time.Time
}

// Create makes a player's tournament for a published game that seats two at
// a table, with the player as its organizer and its first entrant. Every
// problem with the request comes back at once.
func (s *Service) Create(ctx context.Context, playerID string, in CreateRequest) (*Tournament, error) {
	t, err := s.prepare(ctx, in, false)
	if err != nil {
		return nil, err
	}
	t.OrganizerID = &playerID
	if err := s.insert(ctx, t, playerID); err != nil {
		return nil, err
	}
	return s.Get(ctx, AsPlayer(playerID), t.ID)
}

// CreateOfficial is an admin's tournament from the console: public, with no
// organizer and no entrant yet, started by an admin or at StartsAt by the
// clock. The audit log names the admin.
func (s *Service) CreateOfficial(ctx context.Context, adminID string, in CreateRequest) (*Tournament, error) {
	t, err := s.prepare(ctx, in, true)
	if err != nil {
		return nil, err
	}
	t.AdminID = &adminID
	if err := s.insert(ctx, t, ""); err != nil {
		return nil, err
	}
	s.record(ctx, adminID, model.ActionTournamentCreated, t.ID, map[string]any{
		"game": t.GameID, "format": t.Format, "size": t.Size, "code": t.Code,
	})
	return s.Get(ctx, AsAdmin(adminID), t.ID)
}

// prepare checks a request against the game it names and builds the row.
// The game first (not_found), then every field problem in one refusal.
func (s *Service) prepare(ctx context.Context, in CreateRequest, official bool) (*model.Tournament, error) {
	g, err := gameRow(ctx, s.db, in.Game)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if g == nil || g.Listing != enum.Published {
		return nil, errNotFound
	}

	bad := map[string]string{}
	block := g.Room
	if block.Players.Max < 2 {
		bad["game"] = "not_multiplayer"
	}

	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		bad["name"] = "required"
	case model.TooLong(name, model.MaxTournamentName):
		bad["name"] = "too_long"
	}

	t := &model.Tournament{
		GameID: g.ID, Name: name, Status: enum.TournamentOpen, Config: block,
		BestOf: 1, CreatedAt: data.Now(),
	}
	if t.Config.State == "" {
		t.Config.State = enum.StateBlob
	}

	switch enum.TournamentFormat(in.Format) {
	case enum.FormatKnockout:
		t.Format = enum.FormatKnockout
		if block.Players.Min > 2 {
			bad["format"] = "needs_duels"
		}
		t.BestOf = in.BestOf
		if t.BestOf == 0 {
			t.BestOf = 1
		}
		if t.BestOf != 1 && t.BestOf != 3 && t.BestOf != 5 {
			bad["best_of"] = "1_3_or_5"
		}
		t.Size = in.Size
		if t.Size == 0 {
			t.Size = min(defaultSize, s.cfg.MaxEntrants)
		}
		if t.Size < 2 || t.Size > s.cfg.MaxEntrants {
			bad["size"] = "out_of_range"
		}
	case enum.FormatLeague:
		t.Format = enum.FormatLeague
		t.Rounds = in.Rounds
		if t.Rounds == 0 {
			t.Rounds = defaultRounds
		}
		if t.Rounds < 1 || t.Rounds > model.MaxLeagueRounds {
			bad["rounds"] = "1_to_20"
		}
		lo, hi := s.leagueTable(block)
		t.Size = in.Size
		if t.Size == 0 {
			t.Size = hi
		}
		if t.Size < lo || t.Size > hi {
			bad["size"] = "out_of_range"
		}
	default:
		bad["format"] = "unknown"
	}

	switch v := enum.TournamentVisibility(in.Visibility); {
	case official:
		t.Visibility = enum.TournamentPublic
	case v == "" || v == enum.TournamentPrivate:
		t.Visibility = enum.TournamentPrivate
	case v == enum.TournamentFriends:
		t.Visibility = enum.TournamentFriends
	case v == enum.TournamentPublic:
		bad["visibility"] = "not_allowed"
	default:
		bad["visibility"] = "unknown"
	}

	if official && in.StartsAt != nil {
		at := in.StartsAt.UTC().Truncate(time.Second)
		if !at.After(t.CreatedAt) {
			bad["starts_at"] = "in_the_past"
		}
		t.StartsAt = &at
	}

	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	return t, nil
}

// leagueTable is the smallest and the largest a league of the game may be:
// two, or the game's own minimum if it is more, up to its table (and the
// relay's).
func (s *Service) leagueTable(block model.RoomBlock) (lo, hi int) {
	lo = max(2, block.Players.Min)
	hi = min(block.Players.Max, s.cfg.MaxMembers, maxEntrants)
	return lo, hi
}

// insert writes a new tournament with a fresh code, and its organizer as its
// first entrant when one plays.
func (s *Service) insert(ctx context.Context, t *model.Tournament, entrant string) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		placed := false
		var err error
		for range codeAttempts {
			t.ID, t.Code = ids.New(ids.Tournament), s.newCode()
			if placed, err = insertTournament(ctx, s.db, t); err != nil {
				return err
			}
			if placed {
				break
			}
		}
		if !placed {
			return fmt.Errorf("tournaments: no free code after %d draws", codeAttempts)
		}
		if entrant == "" {
			return nil
		}
		return data.Conn(ctx, s.db).Create(&model.TournamentEntrant{
			TournamentID: t.ID, PlayerID: entrant, Seed: 1, JoinedAt: t.CreatedAt,
		}).Error
	})
	if err != nil {
		return asError(err)
	}
	return nil
}

// gameRow is a game whatever its listing: a tournament made before a
// suspension still has a name to show.
func gameRow(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var g model.Game
	err := data.Conn(ctx, db).Take(&g, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}
