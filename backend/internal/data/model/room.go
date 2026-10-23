package model

// What a room is, with no idea of the sockets that carry it.

import (
	"database/sql/driver"
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// RoomCodeLength is how many symbols a room code has. Six of a 32-symbol
// alphabet is about a billion codes, of which only the open rooms hold any.
const RoomCodeLength = 6

// Room is one room: a game, a host, a code, and the manifest's room block as
// it stood when the room was made. The relay holds the live side in Redis;
// this row is what membership hangs on and what is left when it ends.
type Room struct {
	ID         string `gorm:"primaryKey"`
	Code       string
	GameID     string
	HostID     string
	Status     enum.RoomStatus
	Visibility enum.Visibility

	// LAN says the host asked for a Wi-Fi relay. The phones discover one; the
	// platform only remembers it was asked.
	LAN bool `gorm:"column:lan"`

	// MatchID is the tournament match this room was made to play
	// (docs/TOURNAMENTS.md); nil for every other room. Only the match's
	// players come in, and its rounds are the match's.
	MatchID *string

	// Config is the game's room block at creation: bounds, host mode, pace,
	// state mode, rejoin. Copied, never read back from the game.
	Config RoomBlock

	// State and StateVersion are the shared state as it stood when the room
	// ended. While it is open the relay keeps the live copy; nil until then.
	State        NullRaw
	StateVersion int

	// Round is how many rounds have started: 0 in a fresh lobby, the round
	// being played while playing, the round just finished back in the lobby.
	// A finished round puts the room back in its lobby; it only grows.
	Round int

	// EndReason is the room's own end. Results is the copy of its latest
	// round's results, as that round's finished or ended frame carried them;
	// the results table is the record.
	EndReason *enum.EndReason
	Results   Scores

	CreatedAt time.Time
	ActiveAt  time.Time
	// StartedAt is the current round's start, or the last one's; EndedAt is
	// the room's own end.
	StartedAt *time.Time
	EndedAt   *time.Time
}

func (Room) TableName() string { return "rooms" }

// Open is a room a player can still be in.
func (r *Room) Open() bool { return r.Status.Open() }

// Authoritative says only the host may write the shared state.
func (r *Room) Authoritative() bool { return r.Config.Host == enum.HostAuthoritative }

// RoomMember is a player's place in a room. The row outlives a leave and a
// kick, so a rejoin can be told from a stranger and a kick lasts.
type RoomMember struct {
	RoomID   string `gorm:"primaryKey"`
	PlayerID string `gorm:"primaryKey"`
	Role     enum.RoomRole
	Seat     int
	JoinedAt time.Time

	// LeftAt is set by a leave and by a kick; Kicked says which, and a kicked
	// player never comes back to this room.
	LeftAt *time.Time
	Kicked bool
}

func (RoomMember) TableName() string { return "room_members" }

// Current is a member who has neither left nor been kicked.
func (m *RoomMember) Current() bool { return m.LeftAt == nil }

// Score is one player's line in a room's results. Rank is 1 for the best;
// ties share a rank.
type Score struct {
	Player string  `json:"player"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
}

// Scores is a round's results, one jsonb column. Nil is a room no round of
// which has finished, written as NULL; a finish or an end with no scores is
// an empty list.
type Scores []Score

func (s *Scores) Scan(src any) error { return scanJSON(src, s) }

// GormDataType names the column's type: a list of structs whose zero value
// writes NULL would otherwise read to GORM as a relation.
func (Scores) GormDataType() string { return "jsonb" }

func (s Scores) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return valueJSON(s)
}

// RoomEvent is one line of logs.room_events: a join, a leave, a kick, a
// start, an end, a new host. Every transition writes one, in the same
// transaction as the transition, so the history and the room never disagree.
type RoomEvent struct {
	ID     string `gorm:"primaryKey"`
	At     time.Time
	RoomID string
	GameID string

	// PlayerID is who the event is about: the joiner, the leaver, the kicked,
	// the new host, the host who started, finished or ended it. Empty when the
	// clock ended the room or finished its round.
	PlayerID string
	Event    enum.RoomEvent
	Detail   Raw
}

func (RoomEvent) TableName() string { return "logs.room_events" }
