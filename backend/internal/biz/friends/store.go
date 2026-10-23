package friends

// The queries, as functions over a connection: inside a transaction they
// join it (data.Conn), outside it they use the pool. A pair's row is keyed
// lower id first; every change to a pair is made holding the pair's lock
// (lockPair), so two answers at once cannot both find the row as it was.

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
)

// lockPair serialises everything that changes one pair: a request, an
// answer, a removal, a block. Taken whichever way round the pair is named.
func lockPair(ctx context.Context, db *gorm.DB, a, b string) error {
	lo, hi := model.Pair(a, b)
	return data.Conn(ctx, db).Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "friends:"+lo+":"+hi).Error
}

// friendship is the pair's row, or nil. Find rather than Take: no row is
// the common answer, not one to log.
func friendship(ctx context.Context, db *gorm.DB, a, b string) (*model.Friendship, error) {
	lo, hi := model.Pair(a, b)
	var rows []model.Friendship
	err := data.Conn(ctx, db).Where("player_lo = ? AND player_hi = ?", lo, hi).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func deleteFriendship(ctx context.Context, db *gorm.DB, a, b string) error {
	lo, hi := model.Pair(a, b)
	return data.Conn(ctx, db).Where("player_lo = ? AND player_hi = ?", lo, hi).Delete(&model.Friendship{}).Error
}

// blocksBetween says who blocked whom: mine is a blocking b, theirs is b
// blocking a.
func blocksBetween(ctx context.Context, db *gorm.DB, a, b string) (mine, theirs bool, err error) {
	var rows []model.Block
	err = data.Conn(ctx, db).Where("(blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?)", a, b, b, a).
		Find(&rows).Error
	for _, r := range rows {
		if r.BlockerID == a {
			mine = true
		} else {
			theirs = true
		}
	}
	return mine, theirs, err
}

// cardRow is a player as a list draws them, with the time that orders the
// list: the accept for a friend, the request for a request.
type cardRow struct {
	ID          string
	Nickname    string
	Handle      string
	AvatarKey   string
	At          *time.Time
	RequestedBy string
}

// accepted is the player's friends, with when each became one. A player an
// admin switched off is left out.
func accepted(ctx context.Context, db *gorm.DB, playerID string) ([]cardRow, error) {
	var rows []cardRow
	err := data.Conn(ctx, db).Raw(`
		SELECT p.id, p.nickname, p.handle, p.avatar_key, COALESCE(f.responded_at, f.created_at) AS at
		FROM (
			SELECT player_hi AS other, responded_at, created_at FROM friendships WHERE player_lo = ? AND status = ?
			UNION ALL
			SELECT player_lo, responded_at, created_at FROM friendships WHERE player_hi = ? AND status = ?
		) f JOIN players p ON p.id = f.other
		WHERE NOT p.disabled`,
		playerID, enum.FriendshipAccepted, playerID, enum.FriendshipAccepted).Scan(&rows).Error
	return rows, err
}

// pending is every request the player is in, either way, newest first.
func pending(ctx context.Context, db *gorm.DB, playerID string) ([]cardRow, error) {
	var rows []cardRow
	err := data.Conn(ctx, db).Raw(`
		SELECT p.id, p.nickname, p.handle, p.avatar_key, f.created_at AS at, f.requested_by
		FROM (
			SELECT player_hi AS other, created_at, requested_by FROM friendships WHERE player_lo = ? AND status = ?
			UNION ALL
			SELECT player_lo, created_at, requested_by FROM friendships WHERE player_hi = ? AND status = ?
		) f JOIN players p ON p.id = f.other
		WHERE NOT p.disabled
		ORDER BY f.created_at DESC, p.id`,
		playerID, enum.FriendshipRequested, playerID, enum.FriendshipRequested).Scan(&rows).Error
	return rows, err
}

// friendsOf is the subquery of a player's friends' ids: two placeholders.
const friendsOf = `
	SELECT player_hi AS id FROM friendships WHERE player_lo = ? AND status = 'accepted'
	UNION ALL
	SELECT player_lo FROM friendships WHERE player_hi = ? AND status = 'accepted'`

// mutual counts the friends a and b share, leaving out the switched off.
func mutual(ctx context.Context, db *gorm.DB, a, b string) (int, error) {
	var n int64
	err := data.Conn(ctx, db).Raw(`
		SELECT count(*) FROM (`+friendsOf+`) x
		JOIN (`+friendsOf+`) y ON y.id = x.id
		JOIN players p ON p.id = x.id
		WHERE NOT p.disabled`, a, a, b, b).Scan(&n).Error
	return int(n), err
}

// among is which of others are the player's friends with no block between
// the two, either way.
func among(ctx context.Context, db *gorm.DB, playerID string, others []string) ([]string, error) {
	var out []string
	err := data.Conn(ctx, db).Raw(`
		SELECT f.other FROM (
			SELECT player_hi AS other FROM friendships WHERE player_lo = ? AND player_hi IN ? AND status = ?
			UNION ALL
			SELECT player_lo FROM friendships WHERE player_hi = ? AND player_lo IN ? AND status = ?
		) f
		WHERE NOT EXISTS (
			SELECT 1 FROM blocks b
			WHERE (b.blocker_id = ? AND b.blocked_id = f.other) OR (b.blocker_id = f.other AND b.blocked_id = ?)
		)`,
		playerID, others, enum.FriendshipAccepted, playerID, others, enum.FriendshipAccepted,
		playerID, playerID).Scan(&out).Error
	return out, err
}

// roomRow is the open room a player is a current member of, with its game.
type roomRow struct {
	PlayerID    string
	RoomID      string
	Code        string
	GameID      string
	GameName    string
	GameIconKey string
}

// openRooms is, for each (player, room) the presence keys named, the room
// when it is open and the player still a current member of it.
func openRooms(ctx context.Context, db *gorm.DB, playerIDs, roomIDs []string) ([]roomRow, error) {
	var rows []roomRow
	err := data.Conn(ctx, db).Raw(`
		SELECT m.player_id, r.id AS room_id, r.code, r.game_id, g.name AS game_name, g.icon_key AS game_icon_key
		FROM room_members m
		JOIN rooms r ON r.id = m.room_id
		JOIN games g ON g.id = r.game_id
		WHERE m.player_id IN ? AND m.room_id IN ? AND m.left_at IS NULL AND r.status <> ?`,
		playerIDs, roomIDs, enum.RoomEnded).Scan(&rows).Error
	return rows, err
}

// publishedGame is a game a player may see, or nil.
func publishedGame(ctx context.Context, db *gorm.DB, id string) (*model.Game, error) {
	var rows []model.Game
	err := data.Conn(ctx, db).Where("id = ? AND listing = ?", id, enum.Published).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// standingRow is one player's results in a game, summed up.
type standingRow struct {
	PlayerID     string
	Plays        int
	Wins         int
	Best         float64
	LastPlayedAt time.Time
	Nickname     string
	Handle       string
	AvatarKey    string
}

// standings is the player's and their friends' results in one game, one
// row each, for those who have any.
func standings(ctx context.Context, db *gorm.DB, playerID, gameID string) ([]standingRow, error) {
	var rows []standingRow
	err := data.Conn(ctx, db).Raw(`
		WITH circle AS (
			SELECT CAST(? AS varchar) AS id
			UNION
			SELECT id FROM (`+friendsOf+`) f
		)
		SELECT r.player_id, count(*) AS plays, count(*) FILTER (WHERE r.rank = 1) AS wins,
		       max(r.score) AS best, max(r.finished_at) AS last_played_at,
		       p.nickname, p.handle, p.avatar_key
		FROM results r
		JOIN circle c ON c.id = r.player_id
		JOIN players p ON p.id = r.player_id
		WHERE r.game_id = ? AND NOT p.disabled
		GROUP BY r.player_id, p.nickname, p.handle, p.avatar_key`,
		playerID, playerID, playerID, gameID).Scan(&rows).Error
	return rows, err
}
