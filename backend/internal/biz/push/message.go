package push

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/jamaa/gathering/backend/internal/data/enum"
	"github.com/jamaa/gathering/backend/internal/data/model"
	"github.com/jamaa/gathering/backend/internal/pkg/fcm"
)

// Notice is a notification as a push reads it: the inbox row, and its
// recipient's unread count now that it is written.
type Notice struct {
	ID       string
	PlayerID string
	Kind     enum.NoticeKind
	GameID   *string
	Title    string
	Body     string
	DeepLink string
	Data     model.Raw
	Key      *string
	Unread   int
}

// The message's fixed parts, as the app is built to them.
const (
	priority = "HIGH"
	channel  = "inbox" // the app makes it, and sets it as Firebase's default
	sound    = "default"

	// maxCollapse is APNs' limit on apns-collapse-id, in bytes.
	maxCollapse = 64
)

// carried is what a row's data may take onto the phone, beside the
// notification's id, its kind and its link. A key the row does not have is
// left out.
var carried = []string{"room", "code", "game", "player", "announcement", "tournament", "match"}

// Message is a notification pushed to one phone: docs/PUSH.md, "The message".
// The title and body are the row's own, in the language they were written
// in; the badge is the recipient's unread count.
func Message(n Notice, token string) fcm.Message {
	collapse := Collapse(n)
	badge := n.Unread
	m := fcm.Message{
		Notification: &fcm.Notification{Title: n.Title, Body: n.Body},
		Data:         DataOf(n),
		Android: &fcm.AndroidConfig{
			Priority:     priority,
			Notification: &fcm.AndroidNotification{ChannelID: channel, Tag: collapse},
		},
		APNS: &fcm.APNSConfig{
			Headers: map[string]string{"apns-collapse-id": collapse},
			Payload: &fcm.APNSPayload{Aps: fcm.Aps{Sound: sound, Badge: &badge}},
		},
	}
	m.To(token)
	return m
}

// DataOf is what the app acts on when the push is tapped: strings only, and
// only the keys that apply. notification and kind always; deep_link when
// the row has a link; room, code, game, player, announcement, tournament and
// match as its data has them, and game from the row's own game when its data names none (a
// game's notification).
func DataOf(n Notice) map[string]string {
	out := map[string]string{"notification": n.ID, "kind": string(n.Kind)}
	if n.DeepLink != "" {
		out["deep_link"] = n.DeepLink
	}
	var fields map[string]any
	_ = json.Unmarshal(n.Data, &fields)
	for _, k := range carried {
		if s, ok := fields[k].(string); ok && s != "" {
			out[k] = s
		}
	}
	if _, named := out["game"]; !named && n.GameID != nil && *n.GameID != "" {
		out["game"] = *n.GameID
	}
	return out
}

// Collapse is what the phone collapses pushes on, so a notification raised
// again replaces its earlier push instead of piling up: the row's key when
// the platform wrote it (room_invite:{room}, friend_request:{player}…), else
// the row's id. A game's keys are its own, per player and game, so its
// notification collapses on the id, which the key's upsert keeps: a
// repeated key still replaces its push, and two games' keys never meet on
// one phone. Cut to 64 bytes at a character.
func Collapse(n Notice) string {
	c := n.ID
	if n.Key != nil && *n.Key != "" && n.Kind != enum.NoticeGame {
		c = *n.Key
	}
	return cut(c, maxCollapse)
}

// cut is s in at most n bytes, never splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
