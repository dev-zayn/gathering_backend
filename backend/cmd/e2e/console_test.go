//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// The console's rooms (docs/CONSOLE.md, "Rooms"): a room playing its first
// round with both members connected, found in the console's list by its game
// with both connected, read whole, then closed from the console: each
// member's socket hears ended with nobody named and the reason admin and
// closes 4004, the members read the room ended by the console, a second
// close is refused, the audit log keeps it, and the player's record and the
// dashboard carry the new numbers.
func TestConsoleRooms(t *testing.T) {
	id := "sweep." + strconv.FormatInt(time.Now().UnixNano(), 36)
	studio := serveStudio(t, id)

	admin := newClient(t)
	admin.origin = console
	var me struct {
		ID string `json:"id"`
	}
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	admin.call("GET", "/v1/admin/me", nil, 200, &me)
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, nil)
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "غرفة الإدارة", "category": "brain"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the console sweep is over"}, 200, nil)
	})

	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")
	aID, bID := pa["id"].(string), pb["id"].(string)
	var room roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": id}, 201, &room)
	b.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code}, 200, nil)
	a.call("POST", "/v1/rooms/"+room.ID+"/start", nil, 200, nil)
	sa := connect(t, a, room.ID, 0, false)
	sb := connect(t, b, room.ID, 0, true)
	sa.expect("hello")
	sb.expect("hello")

	// The list, by the game: the room playing round 1, both members connected
	// (presence is written as each socket opens, so it may take a moment).
	var page consoleRoomsJSON
	var row *consoleRoomJSON
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		admin.call("GET", "/v1/admin/rooms?game="+id, nil, 200, &page)
		if len(page.Items) == 1 && page.Items[0].Connected == 2 {
			row = &page.Items[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the console's list: %+v", page)
		}
	}
	if row.ID != room.ID || row.Code != room.Code || row.Status != "playing" || row.Round != 1 || row.Members != 2 ||
		row.Game.ID != id || row.Host.ID != aID || row.Host.Nickname != "زين" || page.Live.Playing < 1 || row.EndReason != nil {
		t.Fatalf("the room in the console's list: %+v (live %+v)", row, page.Live)
	}
	var refused errorJSON
	admin.call("GET", "/v1/admin/rooms?status=nope&limit=0", nil, 422, &refused)
	if refused.Fields["status"] != "unknown" || refused.Fields["limit"] != "1_to_100" {
		t.Fatalf("the list's refusals: %+v", refused)
	}
	t.Logf("ok   the console lists the room: playing round 1, 2 members, 2 connected (live: %d lobby, %d playing)",
		page.Live.Lobby, page.Live.Playing)

	var detail consoleRoomDetailJSON
	admin.call("GET", "/v1/admin/rooms/"+room.ID, nil, 200, &detail)
	if detail.ID != room.ID || len(detail.Members) != 2 || !detail.Members[0].Connected || !detail.Members[1].Connected ||
		detail.Members[0].ID != aID || detail.Members[0].Role != "host" || detail.Members[1].ID != bID ||
		detail.Config.Max != 2 || len(detail.Events) == 0 || detail.Events[0].Event != "started" {
		t.Fatalf("the room in the console: %+v", detail)
	}
	var record playerRecordJSON
	admin.call("GET", "/v1/admin/players/"+aID, nil, 200, &record)
	if record.ID != aID || record.Room == nil || record.Room.ID != room.ID || record.Room.Code != room.Code ||
		record.Friends != 0 || record.Rounds != 0 {
		t.Fatalf("A's record while in the room: %+v", record)
	}
	t.Logf("ok   the console reads the room (members connected, its events) and A's record names it")

	// The close: both sockets hear ended, by nobody, for the console's reason.
	var closed consoleRoomDetailJSON
	admin.call("POST", "/v1/admin/rooms/"+room.ID+"/close", nil, 200, &closed)
	if closed.Status != "ended" || closed.EndReason == nil || *closed.EndReason != "admin" || closed.Round != 1 {
		t.Fatalf("closed: %+v", closed)
	}
	for _, s := range []*socket{sa, sb} {
		f := s.expect("ended")
		if f.Reason != "admin" || f.By != nil || f.Round != 1 || f.At == "" {
			t.Fatalf("%s ended: %s", s.who, f.raw)
		}
		if code, reason := s.closed(); code != 4004 || reason != "ended" {
			t.Fatalf("%s closed %d %q", s.who, code, reason)
		}
	}
	t.Logf("ok   the console closed the room: both sockets heard ended (by null, reason admin) and closed 4004")

	var seen roomJSON
	b.call("GET", "/v1/rooms/"+room.ID, nil, 200, &seen)
	if seen.Status != "ended" || seen.EndReason == nil || *seen.EndReason != "admin" {
		t.Fatalf("B reads the room: %+v", seen)
	}
	admin.call("POST", "/v1/admin/rooms/"+room.ID+"/close", nil, 409, &refused)
	if refused.Error != "room_closed" {
		t.Fatalf("closing it again: %+v", refused)
	}
	admin.call("POST", "/v1/admin/rooms/room_nothingnothing00/close", nil, 404, nil)
	var log struct {
		Entries []struct {
			Action  string         `json:"action"`
			Actor   string         `json:"actor"`
			Subject string         `json:"subject"`
			Detail  map[string]any `json:"detail"`
		} `json:"entries"`
	}
	admin.call("GET", "/v1/admin/audit?area=rooms&subject="+room.ID, nil, 200, &log)
	if len(log.Entries) != 1 || log.Entries[0].Action != "room.closed" || log.Entries[0].Actor != me.ID ||
		log.Entries[0].Detail["code"] != room.Code {
		t.Fatalf("the audit log: %+v", log)
	}
	admin.call("GET", "/v1/admin/players/"+aID, nil, 200, &record)
	if record.Room != nil {
		t.Fatalf("A's record after the close: %+v", record)
	}
	var dash struct {
		RoomsLive int64 `json:"rooms_live"`
		Rooms     *struct {
			Lobby   int64 `json:"lobby"`
			Playing int64 `json:"playing"`
			Today   int64 `json:"today"`
		} `json:"rooms"`
		RoundsToday *int64 `json:"rounds_today"`
	}
	admin.call("GET", "/v1/admin/dashboard", nil, 200, &dash)
	if dash.Rooms == nil || dash.RoundsToday == nil || dash.Rooms.Today < 1 || *dash.RoundsToday < 1 ||
		dash.RoomsLive != dash.Rooms.Lobby+dash.Rooms.Playing {
		t.Fatalf("the dashboard: %+v", dash)
	}
	t.Logf("ok   room_closed again; the audit log has room.closed; A is in no room; the dashboard counts %d rooms and %d rounds today",
		dash.Rooms.Today, *dash.RoundsToday)
}

// The console's announcements (docs/CONSOLE.md, "Announcements"): every
// problem at once, a dry run that counts and writes nothing, a send to two
// chosen players (one by @handle, one by id) that lands in both inboxes as a
// system line, the list of what was sent, and the audit log.
func TestConsoleAnnouncements(t *testing.T) {
	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")
	bID := pb["id"].(string)
	chosen := map[string]any{"kind": "players", "players": []string{"@" + pa["handle"].(string), bID}}
	title := "تحديث الليلة " + strconv.FormatInt(time.Now().Unix()%1000, 10)

	var refused errorJSON
	admin.call("POST", "/v1/admin/notifications", map[string]any{
		"audience": map[string]any{"kind": "players", "players": []string{"@nobody_0000"}},
		"title":    " ", "body": strings.Repeat("ب", 241),
	}, 422, &refused)
	if refused.Fields["title"] != "required" || refused.Fields["body"] != "too_long" ||
		refused.Fields["audience.players"] != "nobody" {
		t.Fatalf("the refusals: %+v", refused)
	}

	var reach struct {
		ID    string `json:"id"`
		Reach int    `json:"reach"`
	}
	admin.call("POST", "/v1/admin/notifications", map[string]any{
		"audience": chosen, "title": title, "body": "نرجع بعد ساعة", "dry_run": true,
	}, 200, &reach)
	if reach.Reach != 2 || reach.ID != "" {
		t.Fatalf("the dry run: %+v", reach)
	}
	if feed := inboxOf(t, a, ""); len(feed.Items) != 0 {
		t.Fatalf("the dry run wrote: %+v", feed)
	}
	admin.call("POST", "/v1/admin/notifications", map[string]any{
		"audience": map[string]any{"kind": "all"}, "title": title, "dry_run": true,
	}, 200, &reach)
	if reach.Reach < 2 {
		t.Fatalf("a dry run to everybody: %+v", reach)
	}
	t.Logf("ok   every problem at once; the dry run reaches 2 chosen players (everybody: %d) and writes nothing", reach.Reach)

	var sent struct {
		ID    string `json:"id"`
		Reach int    `json:"reach"`
	}
	admin.call("POST", "/v1/admin/notifications", map[string]any{
		"audience": chosen, "title": title, "body": "نرجع بعد ساعة",
	}, 201, &sent)
	if !strings.HasPrefix(sent.ID, "ann_") || sent.Reach != 2 {
		t.Fatalf("sent: %+v", sent)
	}
	for _, cl := range []*client{a, b} {
		feed := inboxOf(t, cl, "")
		if feed.Unread != 1 || len(feed.Items) != 1 {
			t.Fatalf("the inbox: %+v", feed)
		}
		n := feed.Items[0]
		if n.Kind != "system" || n.Title != title || n.Body != "نرجع بعد ساعة" || n.DeepLink != "" ||
			n.Data["announcement"] != sent.ID || n.Actor != nil || n.Game != nil || n.Read {
			t.Fatalf("the system line: %+v", n)
		}
	}
	t.Logf("ok   the announcement %s landed in both inboxes as a system line", sent.ID)

	var list struct {
		Items []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Reach    int    `json:"reach"`
			Audience struct {
				Kind    string   `json:"kind"`
				Players []string `json:"players"`
			} `json:"audience"`
			Admin struct {
				Name string `json:"name"`
			} `json:"admin"`
		} `json:"items"`
	}
	admin.call("GET", "/v1/admin/notifications?limit=5", nil, 200, &list)
	if len(list.Items) == 0 || list.Items[0].ID != sent.ID || list.Items[0].Reach != 2 || list.Items[0].Title != title ||
		list.Items[0].Audience.Kind != "players" || len(list.Items[0].Audience.Players) != 2 || list.Items[0].Admin.Name != "sweep" {
		t.Fatalf("the announcements sent: %+v", list.Items)
	}
	var log struct {
		Entries []struct {
			Action string         `json:"action"`
			Detail map[string]any `json:"detail"`
		} `json:"entries"`
	}
	admin.call("GET", "/v1/admin/audit?area=players&action=notification.sent&subject="+sent.ID, nil, 200, &log)
	if len(log.Entries) != 1 || log.Entries[0].Detail["reach"] != float64(2) || log.Entries[0].Detail["audience"] != "players" {
		t.Fatalf("the audit log: %+v", log)
	}
	t.Logf("ok   the console lists it first (reach 2, by sweep); the audit log has notification.sent")
}

type consoleRoomJSON struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Status string `json:"status"`
	Round  int    `json:"round"`
	Game   struct {
		ID string `json:"id"`
	} `json:"game"`
	Host struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"host"`
	Members   int     `json:"members"`
	Connected int     `json:"connected"`
	EndReason *string `json:"end_reason"`
}

type consoleRoomsJSON struct {
	Items      []consoleRoomJSON `json:"items"`
	NextCursor *string           `json:"next_cursor"`
	Live       struct {
		Lobby   int `json:"lobby"`
		Playing int `json:"playing"`
	} `json:"live"`
}

type consoleRoomDetailJSON struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Round   int    `json:"round"`
	Members []struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Connected bool   `json:"connected"`
	} `json:"members"`
	Config struct {
		Max int `json:"max"`
	} `json:"config"`
	Events []struct {
		Event string `json:"event"`
	} `json:"events"`
	EndReason *string `json:"end_reason"`
}

type playerRecordJSON struct {
	ID      string `json:"id"`
	Friends int    `json:"friends"`
	Rounds  int    `json:"rounds"`
	Room    *struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	} `json:"room"`
}
