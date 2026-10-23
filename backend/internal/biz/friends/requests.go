package friends

import (
	"context"

	"github.com/jamaa/gathering/backend/internal/biz/inbox"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Request is the caller asking another player to be friends — or, when the
// other has already asked the caller, accepting: two players who each tap
// "add" are friends. It answers where the caller now stands, requested or
// friends, and whether a new request was made.
func (s *Service) Request(ctx context.Context, callerID, targetID string) (standing string, created bool, err error) {
	if callerID == targetID {
		return "", false, errIsYou
	}
	if _, err := s.target(ctx, targetID); err != nil {
		return "", false, err
	}
	me, err := s.me(ctx, callerID)
	if err != nil {
		return "", false, err
	}

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, targetID); err != nil {
			return err
		}
		mine, theirs, err := blocksBetween(ctx, s.db, callerID, targetID)
		if err != nil {
			return err
		}
		// The caller's own block first: it is theirs to know about, and
		// naming it says nothing of the other's.
		if mine {
			return errBlocked
		}
		if theirs {
			return errNotFound
		}

		row, err := friendship(ctx, s.db, callerID, targetID)
		if err != nil {
			return err
		}
		switch {
		case row == nil:
			lo, hi := model.Pair(callerID, targetID)
			err := data.Conn(ctx, s.db).Create(&model.Friendship{
				PlayerLo: lo, PlayerHi: hi, Status: enum.FriendshipRequested,
				RequestedBy: callerID, CreatedAt: data.Now(),
			}).Error
			if err != nil {
				return err
			}
			standing, created = Requested, true
			_, err = s.Inbox.Raise(ctx, inbox.Notice{
				PlayerID: targetID, Kind: enum.NoticeFriendRequest, ActorID: callerID,
				Title: requestTitle, Body: me.Nickname + requestBody,
				Data: map[string]any{"player": callerID}, Key: requestKey(callerID),
			})
			return err
		case row.Status == enum.FriendshipAccepted:
			return errAlreadyFriends
		case row.RequestedBy == callerID:
			return errRequestPending
		default:
			standing = Friends
			return s.accept(ctx, me, targetID)
		}
	})
	if err != nil {
		return "", false, asError(err)
	}
	return standing, created, nil
}

// Accept is the caller saying yes to another player's request.
func (s *Service) Accept(ctx context.Context, callerID, askerID string) error {
	me, err := s.me(ctx, callerID)
	if err != nil {
		return err
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, askerID); err != nil {
			return err
		}
		row, err := friendship(ctx, s.db, callerID, askerID)
		if err != nil {
			return err
		}
		if row == nil || row.Status != enum.FriendshipRequested || row.RequestedBy != askerID {
			return errRequestNotFound
		}
		return s.accept(ctx, me, askerID)
	})
	return errorOrNil(err)
}

// accept turns the asker's request into a friendship, in the caller's
// transaction holding the pair: the asker hears it, and the accepter's own
// notification of the request is answered.
func (s *Service) accept(ctx context.Context, accepter *model.Player, askerID string) error {
	lo, hi := model.Pair(accepter.ID, askerID)
	err := data.Conn(ctx, s.db).Model(&model.Friendship{}).
		Where("player_lo = ? AND player_hi = ?", lo, hi).
		Updates(map[string]any{"status": enum.FriendshipAccepted, "responded_at": data.Now()}).Error
	if err != nil {
		return err
	}
	_, err = s.Inbox.Raise(ctx, inbox.Notice{
		PlayerID: askerID, Kind: enum.NoticeFriendAccepted, ActorID: accepter.ID,
		Title: acceptedTitle, Body: accepter.Nickname + acceptedBody,
		Data: map[string]any{"player": accepter.ID}, Key: acceptedKey(accepter.ID),
	})
	if err != nil {
		return err
	}
	return s.Inbox.Settle(ctx, accepter.ID, "", requestKey(askerID))
}

// Decline is the caller saying no to another player's request. The request
// goes, and its notification with it.
func (s *Service) Decline(ctx context.Context, callerID, askerID string) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, askerID); err != nil {
			return err
		}
		row, err := friendship(ctx, s.db, callerID, askerID)
		if err != nil {
			return err
		}
		if row == nil || row.Status != enum.FriendshipRequested || row.RequestedBy != askerID {
			return errRequestNotFound
		}
		if err := deleteFriendship(ctx, s.db, callerID, askerID); err != nil {
			return err
		}
		return s.Inbox.Withdraw(ctx, callerID, "", requestKey(askerID))
	})
	return errorOrNil(err)
}

// Cancel takes back the caller's own request, and its notification with it.
func (s *Service) Cancel(ctx context.Context, callerID, targetID string) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, targetID); err != nil {
			return err
		}
		row, err := friendship(ctx, s.db, callerID, targetID)
		if err != nil {
			return err
		}
		if row == nil || row.Status != enum.FriendshipRequested || row.RequestedBy != callerID {
			return errRequestNotFound
		}
		if err := deleteFriendship(ctx, s.db, callerID, targetID); err != nil {
			return err
		}
		return s.Inbox.Withdraw(ctx, targetID, "", requestKey(callerID))
	})
	return errorOrNil(err)
}

// Remove ends a friendship. Nothing is raised: nobody is told.
func (s *Service) Remove(ctx context.Context, callerID, friendID string) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if err := lockPair(ctx, s.db, callerID, friendID); err != nil {
			return err
		}
		row, err := friendship(ctx, s.db, callerID, friendID)
		if err != nil {
			return err
		}
		if row == nil || row.Status != enum.FriendshipAccepted {
			return errNotFriends
		}
		return deleteFriendship(ctx, s.db, callerID, friendID)
	})
	return errorOrNil(err)
}

// Request is one request in a list: who, and when it was made.
type Request struct {
	Player players.Card `json:"player"`
	At     string       `json:"at" doc:"When the request was made."`
}

// Requests is a player's requests: the ones made to them, and their own.
type Requests struct {
	Incoming []Request `json:"incoming" doc:"Made to the player, newest first."`
	Outgoing []Request `json:"outgoing" doc:"The player's own, newest first."`
}

// Requests lists the player's open requests, either way, newest first.
func (s *Service) Requests(ctx context.Context, playerID string) (*Requests, error) {
	rows, err := pending(ctx, s.db, playerID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := &Requests{Incoming: []Request{}, Outgoing: []Request{}}
	for _, r := range rows {
		req := Request{Player: s.card(ctx, r)}
		if r.At != nil {
			req.At = stamp(*r.At)
		}
		if r.RequestedBy == playerID {
			out.Outgoing = append(out.Outgoing, req)
		} else {
			out.Incoming = append(out.Incoming, req)
		}
	}
	return out, nil
}

// errorOrNil is asError for a call that answers nothing else.
func errorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return asError(err)
}
