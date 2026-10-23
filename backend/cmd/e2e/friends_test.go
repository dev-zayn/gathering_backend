//go:build e2e

package e2e

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Friends and the inbox: a request, declined, made again and accepted from
// the other side, each step in the inbox with its link, read; two players
// who each ask; the card by handle and by id; a removal; a block and the
// refusals it brings; a friends-only room refusing a stranger and admitting
// a friend; an invite from the lobby whose /r/{code} joins; a friend in a
// room on the friends list; the leaderboard once a room ends with scores;
// and a game's own server raising a notification, its key, its limit and
// the scope it needs.
func TestFriends(t *testing.T) {
	id := "sweep." + strconv.FormatInt(time.Now().UnixNano(), 36)
	studio := serveStudio(t, id, "identity:read", "notify:send", "results:write")

	admin := newClient(t)
	admin.origin = console
	admin.call("POST", "/v1/admin/session/dev", map[string]any{"name": "sweep"}, 200, nil)
	var registered struct {
		Secret string `json:"client_secret"`
	}
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, &registered)
	admin.call("PATCH", "/v1/admin/games/"+id, map[string]any{"tagline": "أصدقاء السويب", "category": "brain"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+id+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
	admin.call("POST", "/v1/admin/games/"+id+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+id+"/suspend", map[string]any{"reason": "the friends sweep is over"}, 200, nil)
	})

	a, pa := player(t, "زين")
	b, pb := player(t, "Mariam")
	c, pc := player(t, "Ali")
	aID, bID, cID := pa["id"].(string), pb["id"].(string), pc["id"].(string)

	// A asks B: a friend_request in B's inbox, and the request in both lists.
	var standing friendshipJSON
	a.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 201, &standing)
	if standing.Friendship != "requested" {
		t.Fatalf("the request: %+v", standing)
	}
	var mine, theirs requestsJSON
	a.call("GET", "/v1/friends/requests", nil, 200, &mine)
	b.call("GET", "/v1/friends/requests", nil, 200, &theirs)
	if len(mine.Outgoing) != 1 || mine.Outgoing[0].Player.ID != bID || len(mine.Incoming) != 0 ||
		len(theirs.Incoming) != 1 || theirs.Incoming[0].Player.ID != aID || theirs.Incoming[0].At == "" {
		t.Fatalf("the lists: %+v / %+v", mine, theirs)
	}
	feed := inboxOf(t, b, "")
	if feed.Unread != 1 || len(feed.Items) != 1 {
		t.Fatalf("B's inbox: %+v", feed)
	}
	if n := feed.Items[0]; n.Kind != "friend_request" || n.Title != "طلب صداقة" || n.Body != "زين يريد يصير صديقك" ||
		n.DeepLink != "" || n.Data["player"] != aID || n.Actor == nil || n.Actor.ID != aID || n.Read {
		t.Fatalf("the friend_request: %+v", n)
	}
	t.Logf("ok   A asked B: requested; a friend_request in B's inbox (no link, data.player A), in both lists")

	// B declines: the request and its notification go.
	b.call("POST", "/v1/friends/requests/"+aID+"/decline", nil, 204, nil)
	if feed := inboxOf(t, b, ""); len(feed.Items) != 0 || feed.Unread != 0 {
		t.Fatalf("B's inbox after the decline: %+v", feed)
	}
	a.call("GET", "/v1/friends/requests", nil, 200, &mine)
	if len(mine.Outgoing) != 0 {
		t.Fatalf("A's requests after the decline: %+v", mine)
	}
	var refused errorJSON
	b.call("POST", "/v1/friends/requests/"+aID+"/decline", nil, 404, &refused)
	if refused.Error != "request_not_found" {
		t.Fatalf("declining twice: %+v", refused)
	}
	t.Logf("ok   B declined: the request and its notification are gone; declining again is request_not_found")

	// A asks again; B accepts from the other side.
	a.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 201, &standing)
	b.call("POST", "/v1/friends/requests/"+aID+"/accept", nil, 200, &standing)
	if standing.Friendship != "friends" {
		t.Fatalf("the accept: %+v", standing)
	}
	feed = inboxOf(t, a, "")
	if feed.Unread != 1 || len(feed.Items) != 1 {
		t.Fatalf("A's inbox: %+v", feed)
	}
	accepted := feed.Items[0]
	if accepted.Kind != "friend_accepted" || accepted.Title != "صرتوا أصدقاء" || accepted.Body != "Mariam قبل طلب صداقتك" ||
		accepted.DeepLink != "" || accepted.Data["player"] != bID || accepted.Actor == nil || accepted.Actor.ID != bID {
		t.Fatalf("the friend_accepted: %+v", accepted)
	}
	if feed := inboxOf(t, b, ""); feed.Unread != 0 || len(feed.Items) != 1 || !feed.Items[0].Read {
		t.Fatalf("B's own request after accepting it: %+v", feed)
	}
	a.call("POST", "/v1/notifications/read", map[string]any{"ids": []string{accepted.ID}}, 204, nil)
	if feed := inboxOf(t, a, "?limit=1"); feed.Unread != 0 || len(feed.Items) != 1 || !feed.Items[0].Read {
		t.Fatalf("A's inbox after reading: %+v", feed)
	}
	a.call("POST", "/v1/friends/requests", map[string]any{"player": bID}, 409, &refused)
	if refused.Error != "already_friends" {
		t.Fatalf("asking a friend: %+v", refused)
	}
	t.Logf("ok   A asked again and B accepted: friends; a friend_accepted in A's inbox, read; B's request marked read")

	// A and C each tap "add": friends at the second tap.
	a.call("POST", "/v1/friends/requests", map[string]any{"player": cID}, 201, &standing)
	c.call("POST", "/v1/friends/requests", map[string]any{"player": aID}, 200, &standing)
	if standing.Friendship != "friends" {
		t.Fatalf("the second tap: %+v", standing)
	}
	t.Logf("ok   A and C each asked: the second request answered 200 friends")

	// The card, by handle and by id: presence between friends only.
	b.call("PUT", "/v1/me/presence", nil, 204, nil)
	var card cardJSON
	a.call("GET", "/v1/players/"+pb["handle"].(string), nil, 200, &card)
	if card.ID != bID || card.Friendship != "friends" || card.MutualFriends != 0 || card.Presence == nil ||
		card.Presence.State != "online" || card.Presence.Room != nil || card.Avatar != "a03" {
		t.Fatalf("B's card for A: %+v", card)
	}
	b.call("GET", "/v1/players/"+cID, nil, 200, &card)
	if card.ID != cID || card.Friendship != "none" || card.MutualFriends != 1 || card.Presence != nil {
		t.Fatalf("C's card for B: %+v", card)
	}
	t.Logf("ok   the card by handle (friends, online) and by id (none, 1 mutual, no presence)")

	// A removes C: nobody is told, and twice is not_friends.
	a.call("DELETE", "/v1/friends/"+cID, nil, 204, nil)
	var list friendsJSON
	c.call("GET", "/v1/friends", nil, 200, &list)
	if len(list.Friends) != 0 {
		t.Fatalf("C's friends after the removal: %+v", list)
	}
	a.call("DELETE", "/v1/friends/"+cID, nil, 404, &refused)
	if refused.Error != "not_friends" {
		t.Fatalf("removing twice: %+v", refused)
	}
	t.Logf("ok   A removed C; removing again is not_friends")

	// A blocks C: C cannot ask or see A; A is told it is A's own block.
	c.call("POST", "/v1/friends/requests", map[string]any{"player": aID}, 201, nil)
	a.call("POST", "/v1/players/"+cID+"/block", nil, 204, nil)
	a.call("GET", "/v1/friends/requests", nil, 200, &mine)
	if len(mine.Incoming) != 0 {
		t.Fatalf("the block left C's request: %+v", mine)
	}
	c.call("POST", "/v1/friends/requests", map[string]any{"player": aID}, 404, &refused)
	if refused.Error != "not_found" {
		t.Fatalf("C asking A: %+v", refused)
	}
	a.call("POST", "/v1/friends/requests", map[string]any{"player": cID}, 403, &refused)
	if refused.Error != "blocked" {
		t.Fatalf("A asking C: %+v", refused)
	}
	c.call("GET", "/v1/players/"+pa["handle"].(string), nil, 404, nil)
	a.call("GET", "/v1/players/"+cID, nil, 200, &card)
	if card.Friendship != "blocked" {
		t.Fatalf("C's card for A: %+v", card)
	}
	a.call("POST", "/v1/players/"+aID+"/block", nil, 422, &refused)
	if refused.Fields["player"] != "is_you" {
		t.Fatalf("blocking oneself: %+v", refused)
	}
	t.Logf("ok   A blocked C: C's request gone; C asking is not_found, A asking is blocked; C cannot see A's card")

	// A friends-only room: C (blocked) and a stranger are refused, B is not.
	d, _ := player(t, "Huda")
	var room roomJSON
	a.call("POST", "/v1/rooms", map[string]any{"game": id, "visibility": "friends"}, 201, &room)
	if room.Visibility != "friends" {
		t.Fatalf("the room: %+v", room)
	}
	for _, cl := range []*client{c, d} {
		cl.call("POST", "/v1/rooms/join", map[string]any{"code": room.Code}, 403, &refused)
		if refused.Error != "friends_only" {
			t.Fatalf("joining a friends-only room: %+v", refused)
		}
	}
	t.Logf("ok   a friends-only room refuses the blocked C and a stranger alike: friends_only")

	// An invite from the lobby: a room_invite whose /r/{code} joins. Asked
	// again, the same line comes back to the top, unread.
	a.call("POST", "/v1/rooms/"+room.ID+"/invites", map[string]any{"players": []string{cID}}, 422, &refused)
	if refused.Fields["players.0"] != "not_friends" {
		t.Fatalf("inviting C: %+v", refused)
	}
	var invited struct {
		Invited []string `json:"invited"`
	}
	a.call("POST", "/v1/rooms/"+room.ID+"/invites", map[string]any{"players": []string{bID}}, 200, &invited)
	if len(invited.Invited) != 1 || invited.Invited[0] != bID {
		t.Fatalf("invited: %+v", invited)
	}
	feed = inboxOf(t, b, "")
	invite := feed.Items[0]
	if invite.Kind != "room_invite" || invite.Title != "دعوة للعب" || invite.Body != "زين يدعوك تلعب لعبة السويب" ||
		invite.DeepLink != "/r/"+room.Code || invite.Data["room"] != room.ID || invite.Data["code"] != room.Code ||
		invite.Data["game"] != id || invite.Actor == nil || invite.Actor.ID != aID || invite.Game == nil ||
		invite.Game.ID != id || invite.Game.Icon == "" || invite.Read || feed.Unread != 1 {
		t.Fatalf("the invite: %+v (unread %d)", invite, feed.Unread)
	}
	b.call("POST", "/v1/notifications/read", map[string]any{"all": true}, 204, nil)
	a.call("POST", "/v1/rooms/"+room.ID+"/invites", map[string]any{"players": []string{bID, bID}}, 200, &invited)
	feed = inboxOf(t, b, "?unread=true")
	if len(feed.Items) != 1 || feed.Items[0].ID != invite.ID || feed.Unread != 1 {
		t.Fatalf("the invite again: %+v", feed)
	}
	code := strings.TrimPrefix(feed.Items[0].DeepLink, "/r/")
	var joined roomJSON
	b.call("POST", "/v1/rooms/join", map[string]any{"code": code}, 200, &joined)
	if joined.ID != room.ID || joined.You == nil {
		t.Fatalf("joining from the invite: %+v", joined)
	}
	a.call("POST", "/v1/rooms/"+room.ID+"/invites", map[string]any{"players": []string{bID}}, 422, &refused)
	if refused.Fields["players.0"] != "already_member" {
		t.Fatalf("inviting a member: %+v", refused)
	}
	t.Logf("ok   A invited B: a room_invite linking /r/%s, back on top unread when asked again; its code joined", code)

	// A friend in a room is on the friends list with the room.
	sb := connect(t, b, room.ID, 0, false)
	sb.expect("hello")
	a.call("GET", "/v1/friends", nil, 200, &list)
	if len(list.Friends) != 1 || list.Friends[0].Player.ID != bID || list.Friends[0].Presence.State != "in_room" ||
		list.Friends[0].Presence.Room == nil || list.Friends[0].Presence.Room.ID != room.ID ||
		list.Friends[0].Presence.Room.Code != room.Code || list.Friends[0].Presence.Room.Game.ID != id ||
		list.Friends[0].Since == "" {
		t.Fatalf("A's friends: %+v", list)
	}
	t.Logf("ok   with a socket open, B is in_room on A's friends list, with the room and its game")

	// Unblocking restores nothing.
	a.call("DELETE", "/v1/players/"+cID+"/block", nil, 204, nil)
	a.call("GET", "/v1/players/"+cID, nil, 200, &card)
	if card.Friendship != "none" {
		t.Fatalf("after the unblock: %+v", card)
	}
	t.Logf("ok   A unblocked C: none, and no friendship back")

	// The room ends with scores: the leaderboard among friends.
	a.call("POST", "/v1/rooms/"+room.ID+"/start", nil, 200, nil)
	a.call("POST", "/v1/rooms/"+room.ID+"/end", map[string]any{"scores": []map[string]any{
		{"player": aID, "score": 3}, {"player": bID, "score": 5},
	}}, 200, nil)
	sb.expect("ended")
	var board boardJSON
	a.call("GET", "/v1/games/"+id+"/leaderboard?scope=friends", nil, 200, &board)
	if board.Game.ID != id || len(board.Entries) != 2 {
		t.Fatalf("the board: %+v", board)
	}
	first, second := board.Entries[0], board.Entries[1]
	if first.Player.ID != bID || first.Rank != 1 || first.Wins != 1 || first.Plays != 1 || first.Best != 5 || first.You ||
		second.Player.ID != aID || second.Rank != 2 || second.Wins != 0 || second.Best != 3 || !second.You ||
		first.LastPlayedAt == "" {
		t.Fatalf("the board: %+v", board)
	}
	a.call("GET", "/v1/games/"+id+"/leaderboard?scope=global", nil, 422, &refused)
	if refused.Fields["scope"] != "friends_only" {
		t.Fatalf("another scope: %+v", refused)
	}
	a.call("GET", "/v1/games/sweep.nothing/leaderboard", nil, 404, nil)
	t.Logf("ok   after the end, the leaderboard: B 1st (1 win, best 5), A 2nd and marked you")

	gameNotifications(t, admin, a, id, registered.Secret)
}

// gameNotifications is the game's own server raising notifications for A
// with the token it holds: a new one (201), the same key again (200, the
// same line), the fields refused, the hourly limit, and a game that never
// asked for notify:send refused for the scope.
func gameNotifications(t *testing.T, admin, a *client, id, secret string) {
	t.Helper()
	server := gameToken(t, a, id, secret)
	var raised struct {
		ID string `json:"id"`
	}
	server.call("POST", "/v1/bridge/notifications", map[string]any{
		"title": "دورك يا بطل", "body": "مريم لعبت، دورك", "deep_link": "/board", "idempotency_key": "turn-1",
	}, 201, &raised)
	firstID := raised.ID
	server.call("POST", "/v1/bridge/notifications", map[string]any{
		"title": "دورك يا بطل", "body": "لا تتأخر", "deep_link": "/board", "idempotency_key": "turn-1",
	}, 200, &raised)
	if raised.ID != firstID {
		t.Fatalf("the same key answered %s, not %s", raised.ID, firstID)
	}
	feed := inboxOf(t, a, "")
	n := feed.Items[0]
	if n.ID != firstID || n.Kind != "game" || n.Title != "دورك يا بطل" || n.Body != "لا تتأخر" || n.DeepLink != "/board" ||
		n.Game == nil || n.Game.ID != id || n.Actor != nil || n.Read {
		t.Fatalf("the game's notification: %+v", n)
	}
	t.Logf("ok   the game's server raised one (201 %s); the same key answered 200 with it, updated, on top", firstID)

	var refused errorJSON
	server.call("POST", "/v1/bridge/notifications", map[string]any{
		"title": strings.Repeat("ع", 61), "deep_link": "https://elsewhere.example/",
	}, 422, &refused)
	if refused.Fields["title"] != "1_to_60" || refused.Fields["deep_link"] != "must_be_a_path" {
		t.Fatalf("bad fields: %+v", refused)
	}
	for i := range 8 {
		server.call("POST", "/v1/bridge/notifications", map[string]any{"title": "رسالة " + strconv.Itoa(i)}, 201, nil)
	}
	var limited struct {
		Error      string `json:"error"`
		RetryAfter int    `json:"retry_after"`
	}
	server.call("POST", "/v1/bridge/notifications", map[string]any{"title": "واحدة زيادة"}, 429, &limited)
	if limited.Error != "too_many_requests" || limited.RetryAfter <= 0 || limited.RetryAfter > 3600 {
		t.Fatalf("the eleventh: %+v", limited)
	}
	t.Logf("ok   the fields are refused at once; the eleventh in the hour is too_many_requests, retry_after %ds", limited.RetryAfter)

	// A game whose manifest never asked for notify:send.
	lite := id + ".lite"
	studio := serveStudio(t, lite, "identity:read")
	var registered struct {
		Secret string `json:"client_secret"`
	}
	admin.call("POST", "/v1/admin/games", map[string]any{"url": studio + "/"}, 201, &registered)
	admin.call("PATCH", "/v1/admin/games/"+lite, map[string]any{"tagline": "بلا إشعارات", "category": "brain"}, 200, nil)
	admin.upload("PUT", "/v1/admin/games/"+lite+"/icon", "icon.png", "image/png", tinyPNG(t), 200, nil)
	admin.call("POST", "/v1/admin/games/"+lite+"/publish", nil, 200, nil)
	t.Cleanup(func() {
		admin.call("POST", "/v1/admin/games/"+lite+"/suspend", map[string]any{"reason": "the friends sweep is over"}, 200, nil)
	})
	other := gameToken(t, a, lite, registered.Secret)
	other.call("POST", "/v1/bridge/notifications", map[string]any{"title": "دورك"}, 403, &refused)
	if refused.Error != "scope_required" || refused.Scope != "notify:send" {
		t.Fatalf("a token without notify:send: %+v", refused)
	}
	t.Logf("ok   a token without notify:send is refused: scope_required, scope notify:send")
}

// gameToken is the game's server holding a game token for the player: the
// player opens the game, the server spends the code with its secret.
func gameToken(t *testing.T, p *client, id, secret string) *client {
	t.Helper()
	var opened struct {
		URL string `json:"url"`
	}
	p.call("POST", "/v1/games/"+id+"/session", nil, 200, &opened)
	server := newClient(t)
	var issued struct {
		AccessToken string `json:"access_token"`
	}
	server.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {codeOf(t, opened.URL)},
		"client_id": {id}, "client_secret": {secret},
	}, 200, &issued)
	server.bearer = issued.AccessToken
	return server
}

// inboxOf is the player's inbox, with the query given.
func inboxOf(t *testing.T, cl *client, query string) feedJSON {
	t.Helper()
	var feed feedJSON
	cl.call("GET", "/v1/notifications"+query, nil, 200, &feed)
	return feed
}

type friendshipJSON struct {
	Friendship string `json:"friendship"`
}

type cardPlayerJSON struct {
	ID        string  `json:"id"`
	Nickname  string  `json:"nickname"`
	Handle    string  `json:"handle"`
	Avatar    string  `json:"avatar"`
	AvatarURL *string `json:"avatar_url"`
}

type requestsJSON struct {
	Incoming []struct {
		Player cardPlayerJSON `json:"player"`
		At     string         `json:"at"`
	} `json:"incoming"`
	Outgoing []struct {
		Player cardPlayerJSON `json:"player"`
		At     string         `json:"at"`
	} `json:"outgoing"`
}

type tileJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

type presenceJSON struct {
	State string `json:"state"`
	Room  *struct {
		ID   string   `json:"id"`
		Code string   `json:"code"`
		Game tileJSON `json:"game"`
	} `json:"room"`
}

type cardJSON struct {
	cardPlayerJSON
	Friendship    string        `json:"friendship"`
	MutualFriends int           `json:"mutual_friends"`
	Presence      *presenceJSON `json:"presence"`
}

type friendsJSON struct {
	Friends []struct {
		Player   cardPlayerJSON `json:"player"`
		Since    string         `json:"since"`
		Presence presenceJSON   `json:"presence"`
	} `json:"friends"`
}

type notificationJSON struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	DeepLink  string          `json:"deep_link"`
	Data      map[string]any  `json:"data"`
	Actor     *cardPlayerJSON `json:"actor"`
	Game      *tileJSON       `json:"game"`
	CreatedAt string          `json:"created_at"`
	Read      bool            `json:"read"`
}

type feedJSON struct {
	Items      []notificationJSON `json:"items"`
	NextCursor *string            `json:"next_cursor"`
	Unread     int                `json:"unread"`
}

type boardJSON struct {
	Game    tileJSON `json:"game"`
	Entries []struct {
		Player       cardPlayerJSON `json:"player"`
		Rank         int            `json:"rank"`
		Plays        int            `json:"plays"`
		Wins         int            `json:"wins"`
		Best         float64        `json:"best"`
		LastPlayedAt string         `json:"last_played_at"`
		You          bool           `json:"you"`
	} `json:"entries"`
}
