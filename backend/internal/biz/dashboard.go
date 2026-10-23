package biz

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

// Dashboard is what the console's first page cannot count from the lists it
// reads: how many players there are, how many are here, how many games are
// in the store, how many rooms are open.
type Dashboard struct {
	db       *gorm.DB
	presence *players.Presence
}

func NewDashboard(db *gorm.DB, presence *players.Presence) *Dashboard {
	return &Dashboard{db: db, presence: presence}
}

// Overview is everything the page asks for here.
type Overview struct {
	// Players is everybody who may sign in.
	Players int64
	// NewToday is players who signed up since midnight UTC.
	NewToday int64
	// Active is players who used the app in the last seven days.
	Active int64
	// Online is players with a live presence this minute.
	Online int

	// Games is what the store shows: published games.
	Games int64
	// RoomsLive is the rooms open right now: in the lobby or playing.
	RoomsLive int64

	// RoomsLobby and RoomsPlaying are the open rooms by status; RoomsToday
	// the rooms made since midnight UTC.
	RoomsLobby   int64
	RoomsPlaying int64
	RoomsToday   int64
	// RoundsToday is the rounds started since midnight UTC, counted from the
	// started room events, which outlive the rooms they were in.
	RoundsToday int64
}

func (d *Dashboard) Overview(ctx context.Context, now time.Time) (*Overview, error) {
	db := d.db.WithContext(ctx)
	out := &Overview{}

	if err := db.Table("players").Where("disabled = false").Count(&out.Players).Error; err != nil {
		return nil, errors.Internal(err)
	}
	midnight := now.UTC().Truncate(24 * time.Hour)
	if err := db.Table("players").Where("created_at >= ?", midnight).Count(&out.NewToday).Error; err != nil {
		return nil, errors.Internal(err)
	}
	err := db.Table("sessions").Where("last_seen_at >= ? AND revoked_at IS NULL", now.AddDate(0, 0, -7)).
		Distinct("player_id").Count(&out.Active).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if err := db.Table("games").Where("listing = ?", enum.Published).Count(&out.Games).Error; err != nil {
		return nil, errors.Internal(err)
	}
	var open []struct {
		Status enum.RoomStatus
		N      int64
	}
	err = db.Raw(`SELECT status, count(*) AS n FROM rooms WHERE status <> ? GROUP BY status`, enum.RoomEnded).
		Scan(&open).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	for _, o := range open {
		switch o.Status {
		case enum.RoomLobby:
			out.RoomsLobby = o.N
		case enum.RoomPlaying:
			out.RoomsPlaying = o.N
		}
	}
	out.RoomsLive = out.RoomsLobby + out.RoomsPlaying
	if err := db.Table("rooms").Where("created_at >= ?", midnight).Count(&out.RoomsToday).Error; err != nil {
		return nil, errors.Internal(err)
	}
	err = db.Table("logs.room_events").Where("event = ? AND at >= ?", enum.EventStarted, midnight).
		Count(&out.RoundsToday).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if d.presence != nil {
		n, err := d.presence.Online(ctx)
		if err != nil {
			return nil, err
		}
		out.Online = n
	}
	return out, nil
}

// Tally is how far one player has got, for the console's record of them: their
// friends, the rounds they have a result in, and the open room they are in.
type Tally struct {
	// Friends is their accepted friendships, whoever is on the other side.
	Friends int64
	// Rounds is their rows in results: the rounds they were ranked in.
	Rounds int64
	// Room is the open room they are a current member of, or nil.
	Room *RoomRef
}

// RoomRef is a room by its id and the code a player types.
type RoomRef struct {
	ID   string
	Code string
}

// Tally counts one player's friends and rounds and finds their open room.
func (d *Dashboard) Tally(ctx context.Context, playerID string) (*Tally, error) {
	db := d.db.WithContext(ctx)
	out := &Tally{}
	err := db.Table("friendships").
		Where("status = ? AND (player_lo = ? OR player_hi = ?)", enum.FriendshipAccepted, playerID, playerID).
		Count(&out.Friends).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if err := db.Table("results").Where("player_id = ?", playerID).Count(&out.Rounds).Error; err != nil {
		return nil, errors.Internal(err)
	}
	var rooms []RoomRef
	err = db.Raw(`
		SELECT r.id, r.code FROM room_members m JOIN rooms r ON r.id = m.room_id
		WHERE m.player_id = ? AND m.left_at IS NULL AND r.status <> ?
		ORDER BY m.joined_at DESC LIMIT 1`, playerID, enum.RoomEnded).Scan(&rooms).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if len(rooms) > 0 {
		out.Room = &rooms[0]
	}
	return out, nil
}
