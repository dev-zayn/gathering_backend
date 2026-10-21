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
// in the store. Rooms join it with their milestone.
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
	// RoomsLive arrives with the rooms.
	RoomsLive int64
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
	if d.presence != nil {
		n, err := d.presence.Online(ctx)
		if err != nil {
			return nil, err
		}
		out.Online = n
	}
	return out, nil
}
