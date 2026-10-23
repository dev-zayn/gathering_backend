package rooms

// The queries, as functions over a connection: inside a transaction they
// join it (data.Conn), outside it they use the pool. Every change to who is
// in a room is made holding the room's row (lockRoom), so seats and counts
// are read and written by one transaction at a time.

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
)

func roomByID(ctx context.Context, db *gorm.DB, id string) (*model.Room, error) {
	var r model.Room
	err := data.Conn(ctx, db).Take(&r, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// lockRoom reads the row and holds it until the transaction ends. Every
// transition takes it first.
func lockRoom(ctx context.Context, db *gorm.DB, id string) (*model.Room, error) {
	var r model.Room
	err := data.Conn(ctx, db).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&r, "id = ?", id).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// lockOpenByCode is the open room holding a code, held like lockRoom. At most
// one: the partial unique index says so.
func lockOpenByCode(ctx context.Context, db *gorm.DB, code string) (*model.Room, error) {
	var r model.Room
	err := data.Conn(ctx, db).Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(&r, "code = ? AND status <> ?", code, enum.RoomEnded).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// openByCode is the open room holding a code, read and not held: what the
// invite link's page shows changes nothing. At most one, as above. Find, not
// Take: anybody may ask the page for any code, and a code nobody holds is
// an everyday answer, not an error for the log.
func openByCode(ctx context.Context, db *gorm.DB, code string) (*model.Room, error) {
	var rows []model.Room
	err := data.Conn(ctx, db).Where("code = ? AND status <> ?", code, enum.RoomEnded).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// endedWithCode says whether a room that has ended held this code. Codes are
// reused once a room ends, so this is only ever "the code you typed was a
// room, and it is over".
func endedWithCode(ctx context.Context, db *gorm.DB, code string) (bool, error) {
	var found bool
	err := data.Conn(ctx, db).Raw(`SELECT EXISTS (SELECT 1 FROM rooms WHERE code = ? AND status = ?)`,
		code, enum.RoomEnded).Scan(&found).Error
	return found, err
}

// lockPlayer serialises everything that decides which open room a player is
// in: two joins at once must not both find the player free.
func lockPlayer(ctx context.Context, db *gorm.DB, playerID string) error {
	return data.Conn(ctx, db).Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "room-player:"+playerID).Error
}

// openRoomOf is the open room the player is a current member of, or "".
func openRoomOf(ctx context.Context, db *gorm.DB, playerID string) (string, error) {
	var ids []string
	err := data.Conn(ctx, db).Raw(`
		SELECT m.room_id FROM room_members m JOIN rooms r ON r.id = m.room_id
		WHERE m.player_id = ? AND m.left_at IS NULL AND r.status <> ?
		ORDER BY m.joined_at DESC LIMIT 1`, playerID, enum.RoomEnded).Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

func membership(ctx context.Context, db *gorm.DB, roomID, playerID string) (*model.RoomMember, error) {
	var m model.RoomMember
	err := data.Conn(ctx, db).Take(&m, "room_id = ? AND player_id = ?", roomID, playerID).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// memberRow is a current member with the card they are drawn with, and
// when they sat down: the console lists it.
type memberRow struct {
	PlayerID  string
	Role      enum.RoomRole
	Seat      int
	Nickname  string
	Handle    string
	AvatarKey string
	JoinedAt  time.Time
}

func currentMembers(ctx context.Context, db *gorm.DB, roomID string) ([]memberRow, error) {
	var rows []memberRow
	err := data.Conn(ctx, db).Raw(`
		SELECT m.player_id, m.role, m.seat, p.nickname, p.handle, p.avatar_key, m.joined_at
		FROM room_members m JOIN players p ON p.id = m.player_id
		WHERE m.room_id = ? AND m.left_at IS NULL
		ORDER BY m.seat`, roomID).Scan(&rows).Error
	return rows, err
}

// everMembers is everybody who was ever in the room, current or not, and the
// seat each last held: who a score may name, and where a result says they sat.
func everMembers(ctx context.Context, db *gorm.DB, roomID string) (map[string]int, error) {
	var rows []struct {
		PlayerID string
		Seat     int
	}
	err := data.Conn(ctx, db).Model(&model.RoomMember{}).Select("player_id, seat").
		Where("room_id = ?", roomID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.PlayerID] = r.Seat
	}
	return out, nil
}

// lowestFreeSeat is the first seat no current member holds.
func lowestFreeSeat(rows []memberRow) int {
	taken := make(map[int]bool, len(rows))
	for _, r := range rows {
		taken[r.Seat] = true
	}
	seat := 0
	for taken[seat] {
		seat++
	}
	return seat
}

// insertRoom writes the row unless an open room already holds the code, and
// says whether it did. ON CONFLICT on the partial index rather than a failed
// insert, so the transaction survives a collision and draws again.
func insertRoom(ctx context.Context, db *gorm.DB, r *model.Room) (bool, error) {
	res := data.Conn(ctx, db).Exec(`
		INSERT INTO rooms (id, code, game_id, host_id, status, visibility, lan, config, match_id, state_version, created_at, active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT (code) WHERE status <> 'ended' DO NOTHING`,
		r.ID, r.Code, r.GameID, r.HostID, r.Status, r.Visibility, r.LAN, r.Config, r.MatchID, r.CreatedAt, r.ActiveAt)
	return res.RowsAffected == 1, res.Error
}

func updateRoom(ctx context.Context, db *gorm.DB, id string, fields map[string]any) error {
	return data.Conn(ctx, db).Model(&model.Room{}).Where("id = ?", id).Updates(fields).Error
}

func updateMember(ctx context.Context, db *gorm.DB, roomID, playerID string, fields map[string]any) error {
	return data.Conn(ctx, db).Model(&model.RoomMember{}).
		Where("room_id = ? AND player_id = ?", roomID, playerID).Updates(fields).Error
}

// gameRow is the game behind a room, whatever its listing now: a room made
// before a suspension still has a name to show.
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

// event writes one line of the room's history, in the transaction of the
// change it records.
func event(ctx context.Context, db *gorm.DB, r *model.Room, playerID string, kind enum.RoomEvent, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return data.Conn(ctx, db).Create(&model.RoomEvent{
		ID: ids.New(ids.RoomEvent), At: data.Now(), RoomID: r.ID, GameID: r.GameID,
		PlayerID: playerID, Event: kind, Detail: raw,
	}).Error
}

// idleLobbies are lobbies untouched since before cutoff, oldest first, one
// pass's worth at a time.
func idleLobbies(ctx context.Context, db *gorm.DB, cutoff time.Time, limit int) ([]model.Room, error) {
	var out []model.Room
	err := data.Conn(ctx, db).Where("status = ? AND active_at < ?", enum.RoomLobby, cutoff).
		Order("active_at").Limit(limit).Find(&out).Error
	return out, err
}

// overlong are rounds started before cutoff and still playing.
func overlong(ctx context.Context, db *gorm.DB, cutoff time.Time, limit int) ([]model.Room, error) {
	var out []model.Room
	err := data.Conn(ctx, db).Where("status = ? AND started_at < ?", enum.RoomPlaying, cutoff).
		Order("started_at").Limit(limit).Find(&out).Error
	return out, err
}

// deleteEnded removes rooms that ended before cutoff, their members with them.
func deleteEnded(ctx context.Context, db *gorm.DB, cutoff time.Time) (int64, error) {
	res := data.Conn(ctx, db).Where("status = ? AND ended_at < ?", enum.RoomEnded, cutoff).Delete(&model.Room{})
	return res.RowsAffected, res.Error
}
