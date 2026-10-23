# Friends, the inbox and invites

The contract the platform (`backend/internal/biz/friends`, `biz/inbox`,
`http/player`, `http/guest`) and the app (`FriendsApi`, `NotificationsApi`,
`FriendsCubit`, the Friends tab, the inbox, the bridge's `friends.*`) both
build to. Milestone M6. As with `ROOMS.md`: when the two sides disagree this
file decides, and when it is wrong it is fixed first.

## Words

- **Friendship**: between two players, either a **request** (one asked, the
  other has not answered) or **accepted**. One row per pair.
- **Block**: one player refusing another, in one direction. A block ends
  any friendship or request between the two and keeps them from making a
  new one, inviting each other, or meeting in a friends-only room.
- **Where I stand** with another player, as the app reads it: `none`,
  `requested` (I asked), `incoming` (they asked), `friends`, `blocked` (I
  blocked them, whatever they did). Being blocked by them is never named:
  their card and a request to them answer `not_found`, as for nobody at
  all, and wherever else a standing is drawn (a member list, a result row)
  it reads as `none`. Nobody is told they were blocked.
- **Inbox**: one feed of **notifications** per player, newest first, each
  read or unread. Kinds: `friend_request`, `friend_accepted`, `room_invite`,
  `game` (from a game's server), `system` (since M8 the console's
  announcements: docs/CONSOLE.md, "Announcements"), `tournament` (an
  invite to one, its start, a match ready, its end or its cancellation:
  docs/TOURNAMENTS.md, "Notifications").
- **Invite**: a member of an open room asking a friend to it. It reaches the
  friend as a `room_invite` notification whose deep link `/r/{code}` joins.

## Storage

`00009_friends.sql`

```sql
friendships(
  player_lo varchar(24) NOT NULL REFERENCES players(id),
  player_hi varchar(24) NOT NULL REFERENCES players(id),
  status text NOT NULL,              -- requested | accepted
  requested_by varchar(24) NOT NULL, -- one of the two
  created_at timestamptz NOT NULL,
  responded_at timestamptz,          -- the accept
  PRIMARY KEY (player_lo, player_hi),
  CHECK (player_lo < player_hi)
)
INDEX ON friendships(player_hi)      -- the pair is looked up from either side

blocks(
  blocker_id varchar(24) NOT NULL REFERENCES players(id),
  blocked_id varchar(24) NOT NULL REFERENCES players(id),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (blocker_id, blocked_id),
  CHECK (blocker_id <> blocked_id)
)
INDEX ON blocks(blocked_id)
```

`00010_notifications.sql`

```sql
notifications(
  id varchar(24) PRIMARY KEY,        -- ntf_ + 16
  player_id varchar(24) NOT NULL REFERENCES players(id),  -- the recipient
  kind text NOT NULL,                -- friend_request | friend_accepted | room_invite | game | system | tournament
  actor_id varchar(24) REFERENCES players(id),            -- who caused it, when a player did
  game_id varchar(64) REFERENCES games(id),
  title text NOT NULL,
  body text NOT NULL DEFAULT '',
  deep_link text NOT NULL DEFAULT '',
  data jsonb NOT NULL DEFAULT '{}',  -- {room, code, game} for an invite, {player} for a request
  key text,                          -- idempotency, per recipient and game
  created_at timestamptz NOT NULL,
  read_at timestamptz
)
UNIQUE INDEX notifications_key ON notifications(player_id, COALESCE(game_id, ''), key) WHERE key IS NOT NULL
INDEX notifications_feed ON notifications(player_id, created_at DESC, id)
INDEX notifications_unread ON notifications(player_id) WHERE read_at IS NULL
```

A raise with a key that already exists for that recipient and game updates
the row in place, keeping its id (title, body, link, data, actor,
`created_at` = now, `read_at` = null) instead of adding one: a second invite
to the same room brings the first back to the top, unread, naming whoever
invited last. Notifications older than 90 days (by `created_at`) are
deleted by the housekeeping job.

## Friends: the rules and the routes (player group, scope `session`)

| Operation | Method and path | Body | Answer | Refusals |
|---|---|---|---|---|
| `friends` | `GET /v1/friends` | | 200 `{friends: [Friend]}` | |
| `friend-requests` | `GET /v1/friends/requests` | | 200 `{incoming: [Request], outgoing: [Request]}` | |
| `friend-request` | `POST /v1/friends/requests` | `{player}` | 201 `{friendship: requested}`, or 200 `{friendship: friends}` when they had already asked | 404 `not_found` (no such player, or they blocked the caller), 422 `invalid_fields` (`player: is_you`), 409 `already_friends` / `request_pending`, 403 `blocked` (the caller blocked them) |
| `friend-request-cancel` | `DELETE /v1/friends/requests/{player}` | | 204 | 404 `request_not_found` |
| `friend-accept` | `POST /v1/friends/requests/{player}/accept` | | 200 `{friendship: friends}` | 404 `request_not_found` |
| `friend-decline` | `POST /v1/friends/requests/{player}/decline` | | 204 | 404 `request_not_found` |
| `friend-remove` | `DELETE /v1/friends/{player}` | | 204 | 404 `not_friends` |
| `player-block` / `player-unblock` | `POST` / `DELETE /v1/players/{player}/block` | | 204 | 404 `not_found`, 422 `player: is_you` |
| `player` (M1, extended) | `GET /v1/players/{ref}` | | 200 Card | 404 `not_found` (also when they blocked the caller and the caller has not blocked them) |
| `game-leaderboard` | `GET /v1/games/{game}/leaderboard?scope=friends` | | 200 `{game, entries: [Entry]}` | 404 `not_found`, 422 `scope: friends_only` |

- `{player}` is a player id (`ply_…`). `{ref}` is a handle or an id (a ref
  shaped like an id is read as one first); the card route answers both, so
  a room's member (who has an id) opens the same card as a search result
  (which has a handle). The caller's own card reads `none`, `0` and `null`.
- `friend-request` refuses in this order: `player: is_you`; `not_found` (no
  such player); `blocked` (the caller blocked them, whatever they did);
  `not_found` (they blocked the caller); `already_friends` /
  `request_pending`.
- A request to someone who has an outstanding request to the caller accepts
  it: two players who each tap "add" are friends.
- A request raises a `friend_request` notification for the other player
  (key `friend_request:{caller}`, title "طلب صداقة", body "{nickname} يريد
  يصير صديقك"); an accept, the automatic one included, raises
  `friend_accepted` for the one who asked (key `friend_accepted:{caller}`,
  title "صرتوا أصدقاء", body "{nickname} قبل طلب صداقتك") and marks the
  accepter's own `friend_request` notification read. Both carry the caller
  as actor, data `{player: caller}` and no deep link (the app opens the
  actor's card). A cancel or a decline deletes the pending `friend_request`
  notification. Nothing is raised for a removal or a block.
- Blocking deletes the pair's friendship row and the `friend_request`
  notifications between them, either way. Unblocking restores nothing.
  Both are idempotent: blocking twice, or unblocking a player who was not
  blocked, is 204.
- A player an admin switched off is left out of every list (friends,
  requests, mutual friends, the leaderboard) and is `not_found` to the card,
  a request and a block; a row with them can still be cancelled, declined
  or removed.

```json
Card (the M1 card, extended):
{ "id": "ply_…", "nickname": "سارة", "handle": "sara_1203", "avatar": "a07",
  "avatar_url": null, "friendship": "none", "mutual_friends": 2,
  "presence": null }

Friend:
{ "player": { "id", "nickname", "handle", "avatar", "avatar_url" },
  "since": "2026-10-21T10:00:00Z",
  "presence": { "state": "in_room", "room": { "id", "code", "game": { "id", "name", "icon" } } } }

Request:
{ "player": { "id", "nickname", "handle", "avatar", "avatar_url" },
  "at": "2026-10-21T10:00:00Z" }

Entry:
{ "player": { … }, "rank": 1, "plays": 7, "wins": 4, "best": 31,
  "last_played_at": "…", "you": true }
```

- `presence` is only ever shown between friends (`null` on the card of a
  non-friend, for anyone): `{state: online | in_room | offline, room}` with
  `room` null unless `in_room`. A friend's room is shown so the app can say
  where they are; joining it still takes its code or an invite. A presence
  naming a room that is no longer open, or that the friend is no longer a
  current member of, reads `online`.
- `friends` is ordered in a room first, then online, then offline, each by
  nickname (then id); `since` is the accept. Requests are newest first, `at`
  being the request. `mutual_friends` counts accepted friends the two share.
- The leaderboard ranks the caller and their friends who have results in the
  game (the caller too, only with results of their own): wins (rank 1)
  descending, then best score descending, then plays descending. Ties share
  a rank (1, 1, 3) and are listed by nickname. `best` is the highest score,
  `plays` the rooms with a result, `last_played_at` the latest finish.
  `scope` defaults to `friends`, the only one in v1; `game` is `{id, name,
  icon}`.

## Rooms (extended)

- `POST /v1/rooms` accepts `visibility: friends`. A friends-only room
  admits, on top of the M4 rules, only the host's friends (403
  `friends_only`); and nobody either side of a block with the host
  (`friends_only` too, so a block is never named). The check comes right
  after `kicked` and before `in_another_room`; the host is the room's
  current one, and a member who left passes it again to come back.
- `POST /v1/rooms/{room}/invites` (`room-invite`) `{players: [id, …]}`
  (1–20, else `players: 1_to_20`; a repeated id is invited once): the
  caller is a current member of the room (403 `not_member`), which is open
  (409 `room_closed`); every invitee is not a current member
  (`players.N: already_member`), is the caller's friend
  (`players.N: not_friends`) and is not blocked either way
  (`players.N: not_friends`), every problem at once and nobody invited
  unless all pass. Each invitee gets a `room_invite` notification: title
  "دعوة للعب", body "{nickname} يدعوك تلعب {game}", deep link `/r/{code}`,
  data `{room, code, game}`, actor the caller, the room's game as its
  `game`, key `room_invite:{room}`. Answers 200 `{invited: [id, …]}`.
- An invite does not reserve a seat. Joining from it is the M4 join by
  code, with every M4 refusal (`room_full`, `room_started`, …).

## The inbox (player group, scope `session`)

| Operation | Method and path | Body | Answer | Refusals |
|---|---|---|---|---|
| `notifications` | `GET /v1/notifications?cursor=&limit=&unread=` | | 200 `{items: [Notification], next_cursor, unread}` | 422 `invalid_fields` (`limit: 1_to_50`, `cursor: invalid`) |
| `notifications-read` | `POST /v1/notifications/read` | `{ids: [id, …]}` or `{all: true}` | 204 | 422 `invalid_fields` (`ids: required`, `ids: up_to_100`) |

The feed is newest first (`created_at` descending, then id); `next_cursor`
is opaque and null on the last page, as the history's. `limit` defaults to
20; `unread=true` lists unread ones only; `unread` in the answer is the
caller's whole unread count (the app's badge reads it from a `limit=1`
call). Marking read takes at most 100 ids; ids that are not the caller's
are ignored, one already read keeps its time, and `all: true` marks every
one whatever `ids` says.

```json
Notification:
{ "id": "ntf_…", "kind": "room_invite", "title": "دعوة للعب",
  "body": "زين يدعوك تلعب إكس أو", "deep_link": "/r/K7M2QX",
  "data": { "room": "room_…", "code": "K7M2QX", "game": "fixture.xo" },
  "actor": { "id", "nickname", "handle", "avatar", "avatar_url" } | null,
  "game": { "id", "name", "icon" } | null,
  "created_at": "…", "read": false }
```

## A game's server raises a notification (guest group)

`POST /v1/bridge/notifications` (`bridge-notify`) with a game token carrying
`notify:send` (else 403 `scope_required`, `scope: notify:send`): `{title,
body?, deep_link?, idempotency_key?}` for the token's player, kind `game`,
game the token's, data `{}`, no actor. `title` 1–60 characters
(`title: 1_to_60`), `body` up to 160 (`body: up_to_160`), `deep_link` a path
inside the game, as the launch's `path` (`deep_link: must_be_a_path`),
`idempotency_key` up to 64 (`idempotency_key: up_to_64`; it becomes the
key), every problem at once. Then the game must still be on the player's
shelf (401 `invalid_token`, as on the context). At most 10 a player per
game per hour, every call that gets this far counting, a reused key
included (429 `too_many_requests` with `retry_after`, in seconds). Answers
201 `{id}`, or 200 `{id}` for a key already used: that notification,
updated in place as any raise (back to the top, unread).

## The bridge (`window.gathering.friends`)

| Call | Answers |
|---|---|
| `friends.status(id)` | `none` · `requested` · `incoming` · `friends` · `blocked`; rejects `not_found` |
| `friends.add(id)` | the app asks the player first in its own sheet ("ترسل طلب صداقة لـ …؟"); `requested`, `friends` (they had asked, or already were), or `cancelled` when the player said no; rejects with the API's code (`blocked`, `not_found`, …) |
| `friends.list()` | `[{id, nickname, avatarUrl, online, inRoom}]`; rejects `scope_required` unless the game's manifest has `friends:read` |
| `friends.invite(id)` | `invited`, into the room the game was opened in; rejects `not_in_room` outside one, else the API's code (`not_friends`, …) |
| `room.invite()` (M4's `not_supported`) | the app's own invite sheet over the room; answers the ids invited (an empty list when the player closed it) |

## The app

- `FriendsApi` and `NotificationsApi` speak the tables above, every refusal
  worded.
- `FriendsCubit` (the fourth singleton): friends, requests and presence,
  read on the hub's mount and on resume, and every 60 s while the Friends
  tab or the Play tab's rail is on screen. `NotificationsCubit` the inbox,
  its unread count read the same way, so a bell shows it.
- The Friends tab: a search by handle (from three characters), the incoming
  requests (accept, decline) and the outgoing (cancel), then the friends
  with their presence ring (in a room, online, offline). A row opens the
  player card.
- The player card (`PlayerCardPage(ref)`): the avatar with presence, the
  nickname, the @handle, mutual friends, the one key that fits where they
  stand (add, cancel, accept and decline, or a friends menu with remove),
  block and unblock behind a menu, and "ادعُ للغرفة" while the caller sits
  in an open room and they are a friend.
- The inbox (`NotificationsPage`), from a bell with the unread count on the
  Play tab's header: by day, newest first; a row marks itself read and
  follows its link: a request or an accept opens the player card, an invite
  joins the room by its code and opens its lobby, a game's notification
  opens the game at its path. "اقرأ الكل" marks everything read.
- Rooms: the lobby's "ادعُ" opens an invite sheet (friends, their presence,
  pick any, send); the create flow offers "للأصدقاء بس" (friends only); the
  Results page's rows offer "أضف" on players who are not friends.
- The Play tab: an "أصدقاؤك المتصلون" rail (online and in-room friends),
  each opening the card.
- The store page shows the friends' leaderboard for the game (the top
  five), and opens the whole of it.
- Push (FCM): every notification is also pushed to the player's phones,
  docs/PUSH.md (the device token route exists since M1).

## Not in M6

Push delivery (since docs/PUSH.md), the `/r/{code}` web page and the
system's app links (M7), spectators, global leaderboards. The console's
announcements (kind `system`) arrived with M8: docs/CONSOLE.md.
