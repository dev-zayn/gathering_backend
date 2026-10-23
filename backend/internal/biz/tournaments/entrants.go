package tournaments

// Entrants coming and going (docs/TOURNAMENTS.md, "Entering and leaving"):
// joining by code, leaving (a withdrawal once it runs), the organizer's
// removal, and invites.

import (
	"context"
	"fmt"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// maxInvites is the most players one invite names, as a room's.
const maxInvites = 20

// Join is by code, typed however a friend read it out. An entrant gets the
// tournament back as it is; anybody else is let in while it is open and has
// room, unless the organizer removed them or it is a friends tournament and
// they are not the organizer's friend.
func (s *Service) Join(ctx context.Context, playerID, typed string) (*Tournament, error) {
	code := normalizeCode(typed)
	if !s.validCode(code) {
		return nil, errors.Invalid(map[string]string{"code": "must_be_6_symbols"})
	}
	var id string
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		t, err := liveByCode(ctx, s.db, code, true)
		if err != nil {
			return err
		}
		if t == nil {
			return s.noLiveCode(ctx, code)
		}
		id = t.ID
		st, err := loadRest(ctx, s.db, t)
		if err != nil {
			return err
		}
		e := st.byPlayer[playerID]
		switch {
		case e != nil && e.Current():
			return nil
		case e != nil && e.Removed:
			return errRemoved
		}
		if t.Visibility == enum.TournamentFriends {
			ok, err := s.friendOfOrganizer(ctx, t, playerID)
			if err != nil {
				return err
			}
			if !ok {
				return errFriendsOnly
			}
		}
		if t.Status == enum.TournamentRunning {
			return errStarted
		}
		if len(st.current()) >= t.Size {
			return errFull
		}
		seed := 1
		for _, other := range st.entrants {
			seed = max(seed, other.Seed+1)
		}
		return data.Conn(ctx, s.db).Create(&model.TournamentEntrant{
			TournamentID: t.ID, PlayerID: playerID, Seed: seed, JoinedAt: data.Now(),
		}).Error
	})
	if err != nil {
		return nil, asError(err)
	}
	return s.Get(ctx, AsPlayer(playerID), id)
}

// noLiveCode is the refusal for a code no live tournament holds: over, or
// never was.
func (s *Service) noLiveCode(ctx context.Context, code string) error {
	finished, err := finishedWithCode(ctx, s.db, code)
	if err != nil {
		return err
	}
	if finished {
		return errClosed
	}
	return errTournamentNotFound
}

// friendOfOrganizer is the friends tournament's door: the organizer's friend,
// with no block either way. An official one has no organizer, and no such
// door.
func (s *Service) friendOfOrganizer(ctx context.Context, t *model.Tournament, playerID string) (bool, error) {
	if t.OrganizerID == nil {
		return true, nil
	}
	if *t.OrganizerID == playerID {
		return true, nil
	}
	if s.Friends == nil {
		return false, nil
	}
	found, err := s.Friends.Among(ctx, *t.OrganizerID, []string{playerID})
	if err != nil {
		return false, err
	}
	return found[playerID], nil
}

// Leave takes the player out. While it is open the row goes, and the code
// lets them back; once it runs it is a withdrawal: every match they still
// have is lost (docs/TOURNAMENTS.md, "Deciding").
func (s *Service) Leave(ctx context.Context, playerID, tournamentID string) (*Tournament, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := load(ctx, s.db, tournamentID)
		if err != nil {
			return err
		}
		if st == nil {
			return errNotFound
		}
		readable, err := s.readable(ctx, st.t, st.byPlayer[playerID], playerID)
		if err != nil {
			return err
		}
		if !readable {
			return errNotFound
		}
		if !st.isCurrent(playerID) {
			return errNotEntrant
		}
		if !st.t.Status.Live() {
			return errClosed
		}
		return s.takeOut(ctx, st, playerID, false)
	})
	if err != nil {
		return nil, asError(err)
	}
	return s.Get(ctx, AsPlayer(playerID), tournamentID)
}

// Remove is the organizer's (or an admin's): as a leave of the player's, and
// for good. While it is open the row stays, marked removed, so the code
// never lets them back.
func (s *Service) Remove(ctx context.Context, by Actor, tournamentID, targetID string) (*Tournament, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		st, err := s.loadAsOrganizer(ctx, by, tournamentID)
		if err != nil {
			return err
		}
		if !st.t.Status.Live() {
			return errClosed
		}
		if by.Player != "" && targetID == by.Player {
			return errors.Invalid(map[string]string{"player": "is_you"})
		}
		if !st.isCurrent(targetID) {
			return errors.Invalid(map[string]string{"player": "not_an_entrant"})
		}
		return s.takeOut(ctx, st, targetID, true)
	})
	if err != nil {
		return nil, asError(err)
	}
	return s.Get(ctx, by, tournamentID)
}

// takeOut is a leave, or a removal, in the caller's transaction: while open
// the row goes (a removal keeps it, marked); once running, a withdrawal.
func (s *Service) takeOut(ctx context.Context, st *state, playerID string, removed bool) error {
	e := st.byPlayer[playerID]
	conn := data.Conn(ctx, s.db)
	if st.t.Status == enum.TournamentOpen {
		if !removed {
			return conn.Where("tournament_id = ? AND player_id = ?", st.t.ID, playerID).
				Delete(&model.TournamentEntrant{}).Error
		}
		e.LeftAt, e.Removed = &st.now, true
		st.touchE(e)
		return st.save(ctx, s.db)
	}
	if err := s.withdraw(ctx, st, e, removed); err != nil {
		return err
	}
	return s.commit(ctx, st)
}

// Invite is a current entrant (or the organizer) asking friends to an open
// tournament. Each invitee must be the caller's friend, with no block either
// way, and not an entrant already; every problem comes back at once and
// nobody is asked unless all pass. Each gets a tournament_invite whose link
// joins by the code; inviting the same player again brings that one back to
// the top, unread. It answers the players invited, each once.
func (s *Service) Invite(ctx context.Context, playerID, tournamentID string, invitees []string) ([]string, error) {
	if len(invitees) < 1 || len(invitees) > maxInvites {
		return nil, errors.Invalid(map[string]string{"players": "1_to_20"})
	}
	t, err := tournamentByID(ctx, s.db, tournamentID, false)
	if err != nil {
		return nil, errors.Internal(err)
	}
	if t == nil {
		return nil, errNotFound
	}
	entrants, err := entrantsOf(ctx, s.db, t.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	byPlayer := map[string]*model.TournamentEntrant{}
	for _, e := range entrants {
		byPlayer[e.PlayerID] = e
	}
	readable, err := s.readable(ctx, t, byPlayer[playerID], playerID)
	if err != nil {
		return nil, asError(err)
	}
	if !readable {
		return nil, errNotFound
	}
	if me := byPlayer[playerID]; (me == nil || !me.Current()) && !t.Organizer(playerID) {
		return nil, errNotEntrant
	}
	switch t.Status {
	case enum.TournamentRunning:
		return nil, errStarted
	case enum.TournamentDone, enum.TournamentCancelled:
		return nil, errClosed
	}

	// Each player once, named by where they first appear.
	first := make(map[string]int, len(invitees))
	unique := make([]string, 0, len(invitees))
	for i, id := range invitees {
		if _, seen := first[id]; seen {
			continue
		}
		first[id] = i
		unique = append(unique, id)
	}
	friends := map[string]bool{}
	if s.Friends != nil {
		if friends, err = s.Friends.Among(ctx, playerID, unique); err != nil {
			return nil, asError(err)
		}
	}
	bad := map[string]string{}
	for _, id := range unique {
		field := fmt.Sprintf("players.%d", first[id])
		if e := byPlayer[id]; e != nil && e.Current() {
			bad[field] = "already_entrant"
		} else if !friends[id] {
			bad[field] = "not_friends"
		}
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}
	if s.Inbox == nil {
		return unique, nil
	}

	g, err := gameRow(ctx, s.db, t.GameID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	inviter, err := s.nickname(ctx, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		for _, id := range unique {
			if err := s.raise(ctx, t, id, playerID, inviteNotice(inviter, t.Name, gameName(g, t.GameID))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, asError(err)
	}
	return unique, nil
}

// nickname is a player's name as a notification writes it.
func (s *Service) nickname(ctx context.Context, playerID string) (string, error) {
	var names []string
	err := data.Conn(ctx, s.db).Raw(`SELECT nickname FROM players WHERE id = ?`, playerID).Scan(&names).Error
	if err != nil || len(names) == 0 {
		return "", err
	}
	return names[0], nil
}
