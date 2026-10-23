package rooms

// The console's side of rooms: every room a page at a time with the live
// counts, one room with its members, its latest results and its recent
// history, and the close an admin makes for everybody in it.
// docs/CONSOLE.md, "Rooms", is the contract.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jamaa/gathering/backend/internal/biz/games"
	"github.com/jamaa/gathering/backend/internal/biz/players"
	"github.com/jamaa/gathering/backend/internal/data"
	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/errors"
	"github.com/jamaa/gathering/backend/internal/pkg/ids"
	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
)

// ConsolePlayer is a player as the console names one: enough to read and to
// search for, nothing to draw.
type ConsolePlayer struct {
	ID       string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname string `json:"nickname" example:"زين"`
	Handle   string `json:"handle" example:"zayn_4821"`
}

// ConsoleRoom is one row of the console's list of rooms.
type ConsoleRoom struct {
	ID         string        `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code       string        `json:"code" example:"K7M2QX"`
	Game       games.Tile    `json:"game"`
	Status     string        `json:"status" enum:"lobby,playing,ended"`
	Round      int           `json:"round" doc:"How many rounds have started."`
	Visibility string        `json:"visibility" enum:"private,friends"`
	Host       ConsolePlayer `json:"host" doc:"The current host, or the last one once the room has ended."`
	Members    int           `json:"members" doc:"The current members."`
	Connected  int           `json:"connected" doc:"The current members with a live socket to this room."`
	CreatedAt  string        `json:"created_at"`
	StartedAt  *string       `json:"started_at" nullable:"true" doc:"The current round's start, or the last one's."`
	EndedAt    *string       `json:"ended_at" nullable:"true"`
	EndReason  *string       `json:"end_reason" nullable:"true" enum:"host,empty,idle,timeout,admin,tournament"`
}

// LiveRooms is the open rooms across the platform, by status.
type LiveRooms struct {
	Lobby   int `json:"lobby"`
	Playing int `json:"playing"`
}

// ConsoleRooms is one page of the list.
type ConsoleRooms struct {
	Items      []ConsoleRoom `json:"items" doc:"Newest first."`
	NextCursor *string       `json:"next_cursor" nullable:"true" doc:"Hand it back as cursor for the next page; null on the last."`
	Live       LiveRooms     `json:"live" doc:"The open rooms by status, whatever the filters."`
}

// ConsoleMember is one current member as the console lists them.
type ConsoleMember struct {
	ID        string `json:"id" example:"ply_7f3k9d2m8q1x4b6n"`
	Nickname  string `json:"nickname" example:"زين"`
	Handle    string `json:"handle" example:"zayn_4821"`
	Role      string `json:"role" enum:"host,player,spectator"`
	Seat      int    `json:"seat"`
	Connected bool   `json:"connected" doc:"A live socket to this room."`
	JoinedAt  string `json:"joined_at"`
}

// RoomEventLine is one line of a room's history in logs.room_events.
type RoomEventLine struct {
	At     string         `json:"at"`
	Event  string         `json:"event" enum:"created,joined,left,kicked,started,ended,host,results,finished"`
	Player *string        `json:"player" nullable:"true" doc:"The player's id; null where nobody was: the clock, an empty room, the console."`
	Detail map[string]any `json:"detail"`
}

// ConsoleRoomDetail is one room for the console: the list's fields with the
// members listed, the room block, the latest round's results and its recent
// history.
type ConsoleRoomDetail struct {
	ID         string          `json:"id" example:"room_7f3k9d2m8q1x4b6n"`
	Code       string          `json:"code" example:"K7M2QX"`
	Game       games.Tile      `json:"game"`
	Status     string          `json:"status" enum:"lobby,playing,ended"`
	Round      int             `json:"round" doc:"How many rounds have started."`
	Visibility string          `json:"visibility" enum:"private,friends"`
	Host       ConsolePlayer   `json:"host"`
	Members    []ConsoleMember `json:"members" doc:"The current members, by seat."`
	Connected  int             `json:"connected" doc:"How many of them have a live socket to this room."`
	Config     RoomConfig      `json:"config" doc:"The game's room block as it stood when the room was made."`
	Results    []Result        `json:"results" doc:"The latest round's results, as the Room has them."`
	Events     []RoomEventLine `json:"events" doc:"The last 50 lines of the room's history, newest first."`
	CreatedAt  string          `json:"created_at"`
	StartedAt  *string         `json:"started_at" nullable:"true"`
	EndedAt    *string         `json:"ended_at" nullable:"true"`
	EndReason  *string         `json:"end_reason" nullable:"true" enum:"host,empty,idle,timeout,admin,tournament"`
}

// The console's list: how long a page may be, and how much history a room
// shows.
const (
	consoleLimit  = 100
	consoleEvents = 50
)

// ConsoleQuery is the list's filters, as the route reads them: a status
// (open, lobby, playing, ended or all; empty is open), one game, the cursor
// of the page before and the page's length.
type ConsoleQuery struct {
	Status string
	Game   string
	Cursor string
	Limit  int
}

// consoleRow is a room with its game's and its host's names joined.
type consoleRow struct {
	model.Room
	GameName     *string
	GameIconKey  *string
	HostNickname *string
	HostHandle   *string
}

// List is the rooms, newest first (created_at descending, then id), a page
// at a time, with the open rooms counted by status whatever the filters.
func (s *Service) List(ctx context.Context, q ConsoleQuery) (*ConsoleRooms, error) {
	bad := map[string]string{}
	where, args := "", []any{}
	switch q.Status {
	case "", "open":
		where, args = "r.status <> ?", []any{enum.RoomEnded}
	case string(enum.RoomLobby), string(enum.RoomPlaying), string(enum.RoomEnded):
		where, args = "r.status = ?", []any{q.Status}
	case "all":
		where = "TRUE"
	default:
		bad["status"] = "unknown"
	}
	if q.Limit < 1 || q.Limit > consoleLimit {
		bad["limit"] = "1_to_100"
	}
	after, ok := readConsoleCursor(q.Cursor)
	if !ok {
		bad["cursor"] = "invalid"
	}
	if len(bad) > 0 {
		return nil, errors.Invalid(bad)
	}

	sql := `
		SELECT r.*, g.name AS game_name, g.icon_key AS game_icon_key,
		       h.nickname AS host_nickname, h.handle AS host_handle
		FROM rooms r
		LEFT JOIN games g ON g.id = r.game_id
		LEFT JOIN players h ON h.id = r.host_id
		WHERE ` + where
	if q.Game != "" {
		sql += ` AND r.game_id = ?`
		args = append(args, q.Game)
	}
	if after != nil {
		sql += ` AND (r.created_at < ? OR (r.created_at = ? AND r.id > ?))`
		args = append(args, after.at, after.at, after.id)
	}
	sql += ` ORDER BY r.created_at DESC, r.id LIMIT ?`
	args = append(args, q.Limit+1)

	var rows []consoleRow
	if err := data.Conn(ctx, s.db).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, errors.Internal(err)
	}
	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}

	members, connected, err := s.headcount(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := &ConsoleRooms{Items: make([]ConsoleRoom, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		item := ConsoleRoom{
			ID: r.ID, Code: r.Code, Game: s.consoleGame(ctx, r), Status: string(r.Status), Round: r.Round,
			Visibility: string(r.Visibility), Host: hostOf(r), Members: members[r.ID], Connected: connected[r.ID],
			CreatedAt: stamp(r.CreatedAt),
		}
		item.StartedAt, item.EndedAt, item.EndReason = times(&r.Room)
		out.Items = append(out.Items, item)
	}
	if more {
		last := rows[len(rows)-1]
		next := consoleCursor(last.CreatedAt, last.ID)
		out.NextCursor = &next
	}
	if out.Live, err = s.live(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// live counts the open rooms by status, across the platform.
func (s *Service) live(ctx context.Context) (LiveRooms, error) {
	var counts []struct {
		Status string
		N      int
	}
	err := data.Conn(ctx, s.db).Raw(`SELECT status, count(*) AS n FROM rooms WHERE status <> ? GROUP BY status`,
		enum.RoomEnded).Scan(&counts).Error
	if err != nil {
		return LiveRooms{}, errors.Internal(err)
	}
	var out LiveRooms
	for _, c := range counts {
		switch enum.RoomStatus(c.Status) {
		case enum.RoomLobby:
			out.Lobby = c.N
		case enum.RoomPlaying:
			out.Playing = c.N
		}
	}
	return out, nil
}

// headcount is how many current members each room of a page has, and how
// many of them have a socket open to it: one read of the members and one of
// presence for the whole page. An ended room has nobody connected, whatever a
// presence key not yet expired says.
func (s *Service) headcount(ctx context.Context, rows []consoleRow) (members, connected map[string]int, err error) {
	members, connected = map[string]int{}, map[string]int{}
	if len(rows) == 0 {
		return members, connected, nil
	}
	roomIDs := make([]string, 0, len(rows))
	open := map[string]bool{}
	for i := range rows {
		roomIDs = append(roomIDs, rows[i].ID)
		open[rows[i].ID] = rows[i].Open()
	}
	var seated []struct {
		RoomID   string
		PlayerID string
	}
	err = data.Conn(ctx, s.db).Raw(`SELECT room_id, player_id FROM room_members WHERE room_id IN ? AND left_at IS NULL`,
		roomIDs).Scan(&seated).Error
	if err != nil {
		return nil, nil, errors.Internal(err)
	}
	var asked []string
	for _, m := range seated {
		members[m.RoomID]++
		if open[m.RoomID] {
			asked = append(asked, m.PlayerID)
		}
	}
	if s.Presence == nil || len(asked) == 0 {
		return members, connected, nil
	}
	states, err := s.Presence.Of(ctx, asked)
	if err != nil {
		// Presence that cannot be read is nobody connected, not a failed page.
		applog.From(ctx).Warn("presence not read for the console's rooms", "err", err)
		return members, connected, nil
	}
	for _, m := range seated {
		if st, ok := states[m.PlayerID]; ok && open[m.RoomID] && st.State == players.InRoom && st.Room == m.RoomID {
			connected[m.RoomID]++
		}
	}
	return members, connected, nil
}

// Detail is one room for the console: who is in it and who is connected,
// its room block, the latest round's results and its last 50 events. A room
// deleted a day after its end is not found here; its results are kept.
func (s *Service) Detail(ctx context.Context, roomID string) (*ConsoleRoomDetail, error) {
	var rows []consoleRow
	err := data.Conn(ctx, s.db).Raw(`
		SELECT r.*, g.name AS game_name, g.icon_key AS game_icon_key,
		       h.nickname AS host_nickname, h.handle AS host_handle
		FROM rooms r
		LEFT JOIN games g ON g.id = r.game_id
		LEFT JOIN players h ON h.id = r.host_id
		WHERE r.id = ?`, roomID).Scan(&rows).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	r := &rows[0]

	seated, err := currentMembers(ctx, s.db, r.ID)
	if err != nil {
		return nil, errors.Internal(err)
	}
	live := map[string]bool{}
	if r.Open() {
		live = s.connected(ctx, r.ID, seated)
	}
	results, err := s.resultsOf(ctx, &r.Room)
	if err != nil {
		return nil, err
	}
	events, err := s.recentEvents(ctx, r.ID)
	if err != nil {
		return nil, err
	}

	out := &ConsoleRoomDetail{
		ID: r.ID, Code: r.Code, Game: s.consoleGame(ctx, r), Status: string(r.Status), Round: r.Round,
		Visibility: string(r.Visibility), Host: hostOf(r), Members: make([]ConsoleMember, 0, len(seated)),
		Config: configOf(r.Config), Results: results, Events: events, CreatedAt: stamp(r.CreatedAt),
	}
	for _, m := range seated {
		out.Members = append(out.Members, ConsoleMember{
			ID: m.PlayerID, Nickname: m.Nickname, Handle: m.Handle, Role: string(m.Role), Seat: m.Seat,
			Connected: live[m.PlayerID], JoinedAt: stamp(m.JoinedAt),
		})
		if live[m.PlayerID] {
			out.Connected++
		}
	}
	out.StartedAt, out.EndedAt, out.EndReason = times(&r.Room)
	return out, nil
}

// recentEvents is a room's last lines in logs.room_events, newest first.
func (s *Service) recentEvents(ctx context.Context, roomID string) ([]RoomEventLine, error) {
	var rows []model.RoomEvent
	err := data.Conn(ctx, s.db).Where("room_id = ?", roomID).Order("at DESC, id DESC").
		Limit(consoleEvents).Find(&rows).Error
	if err != nil {
		return nil, errors.Internal(err)
	}
	out := make([]RoomEventLine, 0, len(rows))
	for _, e := range rows {
		line := RoomEventLine{At: stamp(e.At), Event: string(e.Event), Detail: map[string]any{}}
		if e.PlayerID != "" {
			id := e.PlayerID
			line.Player = &id
		}
		if len(e.Detail) > 0 {
			// A line whose detail cannot be read is still a line that happened.
			_ = json.Unmarshal(e.Detail, &line.Detail)
			if line.Detail == nil {
				line.Detail = map[string]any{}
			}
		}
		out = append(out, line)
	}
	return out, nil
}

// Close is an admin closing an open room for everybody in it: an end as the
// host's, with nobody named and the reason admin. A round under way is
// finished by it, as by every end: its results are the game's when its
// server wrote this round's, else none; in the lobby the end carries the
// latest round's. The relay closes every socket with 4004 once ended is
// delivered, and the audit log names the admin.
func (s *Service) Close(ctx context.Context, adminID, roomID string) (*ConsoleRoomDetail, error) {
	var (
		now     time.Time
		closed  model.Room
		results []model.Score
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := lockRoom(ctx, s.db, roomID)
		if err != nil {
			return err
		}
		if r == nil {
			return errNotFound
		}
		if !r.Open() {
			return errRoomClosed
		}
		if results, err = s.closing(ctx, r); err != nil {
			return err
		}
		// A match room's round under way closes with it: counted.
		if _, err := s.counted(ctx, r, results); err != nil {
			return err
		}
		now, closed = data.Now(), *r
		if err := updateRoom(ctx, s.db, r.ID, s.endFields(ctx, r, now, enum.EndAdmin, model.Scores(results))); err != nil {
			return err
		}
		return event(ctx, s.db, r, "", enum.EventEnded, map[string]any{
			"reason": enum.EndAdmin, "admin": adminID, "round": r.Round,
		})
	})
	if err != nil {
		return nil, asError(err)
	}
	s.end(ctx, roomID, Ended{T: FrameEnded, At: stamp(now), Reason: string(enum.EndAdmin), Round: closed.Round, Results: results})
	s.record(ctx, adminID, model.ActionRoomClosed, roomID, map[string]any{
		"code": closed.Code, "game": closed.GameID, "round": closed.Round,
	})
	return s.Detail(ctx, roomID)
}

// record writes an audit line for a change already made. A log that is down
// must not undo the change or refuse the admin, so a failure is logged.
func (s *Service) record(ctx context.Context, actor, action, subject string, detail map[string]any) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Write(ctx, model.Entry{Actor: actor, Action: action, Subject: subject, Detail: detail}); err != nil {
		applog.From(ctx).Error("audit not written", "action", action, "subject", subject, "err", err)
	}
}

// consoleGame is the room's game as a tile, the icon signed on the way out.
func (s *Service) consoleGame(ctx context.Context, r *consoleRow) games.Tile {
	if r.GameName == nil {
		return games.Tile{ID: r.GameID}
	}
	g := &model.Game{ID: r.GameID, Name: *r.GameName}
	if r.GameIconKey != nil {
		g.IconKey = *r.GameIconKey
	}
	return games.TileOf(ctx, s.Files, r.GameID, g)
}

// hostOf is the room's host by name; an id alone when the row is gone.
func hostOf(r *consoleRow) ConsolePlayer {
	h := ConsolePlayer{ID: r.HostID}
	if r.HostNickname != nil {
		h.Nickname = *r.HostNickname
	}
	if r.HostHandle != nil {
		h.Handle = *r.HostHandle
	}
	return h
}

// times is a room's round start, its end and why, as the API writes them.
func times(r *model.Room) (started, ended, reason *string) {
	if r.StartedAt != nil {
		at := stamp(*r.StartedAt)
		started = &at
	}
	if r.EndedAt != nil {
		at := stamp(*r.EndedAt)
		ended = &at
	}
	if r.EndReason != nil {
		why := string(*r.EndReason)
		reason = &why
	}
	return started, ended, reason
}

// consolePosition is where a page of the list starts: after this creation,
// to the microsecond, and this room.
type consolePosition struct {
	at time.Time
	id string
}

// consoleCursor is the cursor after a room: opaque to the console, the
// room's creation and id underneath.
func consoleCursor(at time.Time, id string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "." + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// readConsoleCursor is a cursor the list wrote, or not one at all. Empty is
// the first page.
func readConsoleCursor(s string) (*consolePosition, bool) {
	if s == "" {
		return nil, true
	}
	if len(s) > maxCursor {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	micros, id, found := strings.Cut(string(raw), ".")
	if !found || !ids.Is(ids.Room, id) {
		return nil, false
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &consolePosition{at: time.UnixMicro(n).UTC(), id: id}, true
}
