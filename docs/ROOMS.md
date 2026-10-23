# Rooms and the relay

The contract the platform (`backend/internal/biz/rooms`, `biz/relay`,
`http/player`, `http/ws`) and the app (`gathering_app/lib/modules/gathering/
services/rooms/`) both build to. When the two disagree, this file is the
tie-breaker; when this file is wrong, fix it here first.

Milestones M4 (rooms and the relay), M5 (results) and rounds (2026-10-21:
a room plays again and again without a new room; *Rounds*, near the end,
is the newest word and amends every section before it where they differ).
What is deliberately left for later is listed last.

## Words

- **Room**: one game, one host, a six-character **code**, a **status**
  (`lobby` → `playing` → back to `lobby` after every round → … → `ended`
  when the room closes), a **round** number, and a shape copied from the
  game's manifest `room` block when the room is made (min/max players, host
  mode, pace, state mode, rejoin, spectators, lan).
- **Round**: one game played in a room, from the host's start to its
  finish. Rounds are numbered from 1. A finished round puts the room back
  in its lobby with the same code, members and sockets, and the host starts
  the next one. See *Rounds*.
- **Member**: a player in a room. Each has a **role** (`host` or `player`;
  `spectator` is reserved) and a **seat** (0-based). The host sits in seat 0
  when the room is made. A member is *current* until they leave or are
  kicked.
- **Open room**: status `lobby` or `playing`. A player is a current member
  of at most one open room.
- **The relay**: one WebSocket per (room, member) on the API, carrying
  **frames** (JSON objects with a `t` field). The game page never opens a
  socket: the app owns it and hands frames to the page through
  `window.gathering.room`.
- **seq**: the room's frame counter. Every frame that is part of the room's
  history carries one; it is the order every member sees, whichever API
  replica relayed it.
- **State**: an optional shared blob per room, `{version, data}`, written
  with compare-and-set. `data` is the game's; the platform never reads it.
  Every round starts it afresh at `{version: 0, data: null}`.

## Storage (migration `00007_rooms.sql`)

The column types follow the rest of the schema (an id is `varchar(24)`, a
game id `varchar(64)`); `logs.room_events` is not in 00007: it has existed
since `00004_logs.sql`, with `rev_` ids.

```sql
rooms(
  id varchar(24) PK,             -- room_ + 16
  code varchar(6) NOT NULL,      -- six symbols of ROOM_CODE_ALPHABET, upper case
  game_id varchar(64) NOT NULL REFERENCES games(id),
  host_id varchar(24) NOT NULL REFERENCES players(id),
  status varchar(8) NOT NULL,    -- lobby | playing | ended
  visibility varchar(8) NOT NULL,-- private | friends (M6, docs/FRIENDS.md)
  lan boolean NOT NULL,          -- asked for a Wi-Fi relay (M7 discovers it)
  config jsonb NOT NULL,         -- the manifest's room block at creation (model.RoomBlock)
  state jsonb,                   -- the last state, written on end (NULL until then)
  state_version integer NOT NULL DEFAULT 0,
  end_reason varchar(8),         -- host | empty | idle | timeout | admin (M8, docs/CONSOLE.md) | tournament (varchar(12) since 00013, docs/TOURNAMENTS.md)
  results jsonb,                 -- the end's results, ranked (the results table, 00008, is the record)
  created_at, active_at timestamptz NOT NULL, started_at, ended_at timestamptz
)
UNIQUE INDEX rooms_code_open ON rooms(code) WHERE status <> 'ended'
INDEX rooms_status_active ON rooms(status, active_at)
INDEX rooms_game_idx ON rooms(game_id)

room_members(
  room_id varchar(24) REFERENCES rooms(id) ON DELETE CASCADE,
  player_id varchar(24) REFERENCES players(id),
  role varchar(16) NOT NULL,     -- host | player
  seat integer NOT NULL,
  joined_at timestamptz NOT NULL,
  left_at timestamptz,           -- set on leave and kick
  kicked boolean NOT NULL DEFAULT false,
  PRIMARY KEY (room_id, player_id)
)
UNIQUE INDEX room_members_seat ON room_members(room_id, seat) WHERE left_at IS NULL
INDEX room_members_player ON room_members(player_id) WHERE left_at IS NULL

logs.room_events(                -- since 00004
  id varchar(24) PK,             -- rev_ + 16
  at timestamptz, room_id, game_id, player_id ('' when the clock or an empty room did it),
  event,                         -- created | joined | left | kicked | started | ended | host | results (M5) | finished (rounds)
  detail jsonb
)
```

A code is drawn with `crypto/rand`, uniformly, and inserted with `ON CONFLICT
(code) WHERE status <> 'ended' DO NOTHING`; a collision draws again (eight
times at most). Everything that changes who is in a room holds the room's row
(`SELECT … FOR UPDATE`); create and join also hold a per-player advisory lock,
so two joins at once cannot leave a player in two open rooms. The event row is
written in the same transaction as the change it records.

`active_at` moves on create, join, start, end, a socket connecting, and at
most once a minute on pings. The sweeper reads it.

## The rules

- **Making a room** copies the game's manifest `room` block into
  `config`. The game must be published. `lan: true` is refused when the
  manifest's `room.lan` is false. `visibility` is `private` (the default:
  anybody with the code) or `friends` (M6: the host's friends alone, see
  docs/FRIENDS.md).
- **One open room per player.** Creating or joining while a current member
  of another open room is refused with `in_another_room`, and the refusal
  names that room (`"room": "room_…"`) so the app can offer to go back to it.
- **Joining** is by code. The code is case-insensitive; spaces and dashes are
  ignored; what is left must be six symbols of the alphabet, or the answer is
  `code: must_be_6_symbols` (the default alphabet has no `O`, `0`, `I` or
  `1`). No open room holding the code is `room_not_found`, unless a room
  that has ended held it (`room_closed`). A join that names its game
  (`game`, *Rooms from inside a game*) is then refused with `another_game`
  when the room holding the code is another game's, even for a current
  member of it. A player who is already a current member gets the room back
  (200, idempotent). Otherwise, in this order:
  - a kicked player is refused with `kicked`, for good;
  - a friends-only room refuses anybody who is not the current host's
    friend, or is either side of a block with the host, with
    `friends_only` (M6, docs/FRIENDS.md);
  - a player still in another open room is refused with `in_another_room`;
  - a playing room refuses with `room_started`, unless `config.rejoin` is
    true and the player was a member who left (not kicked);
  - a room whose current members number `config.max` refuses with
    `room_full`.
  The joiner takes the lowest free seat. A rejoiner takes the lowest free
  seat too (their old one may be taken).
- **Leaving** sets `left_at` and frees the seat. When the host leaves, the
  current member in the lowest seat becomes host (a `host` frame with
  `by: null`); when nobody is left, the room ends with reason `empty`.
  Leaving a room that has already ended only takes the caller out of it (204,
  `you` becomes null): nothing is published, there is nobody to tell.
- **Kicking** is the host's, and never of themselves. The kicked member is
  marked `kicked` and can never rejoin this room.
- **Starting** is the host's, from `lobby` only, and needs at least
  `config.min` current members (`too_few_players`). Every start begins a
  round, the first or the next (*Rounds*).
- **Finishing** a round is the host's, from `playing` only: the room goes
  back to its lobby and stays open (*Rounds*). A game's end is a finish.
- **Ending** closes the room for good. It is the host's, from `lobby` or
  `playing`. While a round plays it may carry scores, which are the round's
  (*Rounds*); in the lobby it carries none. The scores are
  `[{player, score, rank?}]`, each for a current or former member, each
  player once; a missing rank is computed by score, highest first, ties
  sharing a rank (1, 1, 3), over every score sent; a rank the game sent is
  kept as it is (at least 1). The results keep the order they were sent in.
  The scores are kept in `rooms.results`, written to the results table and
  sent in the `ended` frame — unless the game's server has already written
  the room's results, which then stand (see *Results (M5)*). Ending writes the
  live state into `rooms.state` / `state_version` (not for `config.state`
  `none`).
- **Handing over** the host role is the host's, to a current member. Handing
  it to yourself changes nothing: 200, the room as it is, no frame.
- The host-only calls refuse in this order: not a current member
  (`not_member`), the room has ended (`room_closed`), not the host
  (`host_only`), then the call's own refusals.
- **The sweeper** (the API's clock, `SWEEP_INTERVAL`): a lobby room with no
  connected member and `active_at` older than `ROOM_LOBBY_IDLE` ends with
  reason `idle`; a round whose `started_at` is older than `ROOM_MAX_PLAY`
  is finished with reason `timeout` and the room goes back to its lobby
  (*Rounds*; before rounds it ended the room); an ended room older than 24 h is
  deleted with its members. Every end, wherever it comes from, publishes
  `ended` so the sockets close. Each end re-checks its room under the row
  lock, so every replica may sweep at once; a lobby whose presence cannot be
  read is kept.
- Every transition writes one `logs.room_events` row.
- **Closing from the console** (M8, docs/CONSOLE.md): an admin may close
  any open room (`admin-room-close`). It ends as the host's end does, a
  round under way finished with it, with one difference: the reason is
  `admin` and nobody is named (`by: null`). `admin` joins the end reasons
  everywhere they are listed.

## HTTP (player group, scope `session`)

Errors are the API's one shape, `{"error": code, …}`. `in_another_room`
adds `"room"`, the room the player is still in (a new `Room` field on
`*errors.Error`).

| Operation | Method and path | Body | Answer | Refusals |
|---|---|---|---|---|
| `room-create` | `POST /v1/rooms` | `{game, lan?, visibility?}` | 201 Room | 404 `not_found` (game not published), 409 `in_another_room`, 422 `invalid_fields` (`lan: not_allowed`) |
| `rooms-mine` | `GET /v1/rooms/mine` | | 200 `{rooms: [Room]}` (zero or one) | |
| `room` | `GET /v1/rooms/{room}` | | 200 Room | 404 `not_found` (no such room, or the caller was never a member) |
| `room-join` | `POST /v1/rooms/join` | `{code, game?}` | 200 Room | 422 `invalid_fields` (`code: must_be_6_symbols`), 404 `room_not_found`, 409 `room_closed` / `another_game` / `in_another_room` / `room_started` / `room_full`, 403 `kicked` / `friends_only` |
| `room-leave` | `POST /v1/rooms/{room}/leave` | | 204 | 403 `not_member` |
| `room-kick` | `POST /v1/rooms/{room}/kick` | `{player}` | 204 | 403 `not_member` / `host_only`, 409 `room_closed`, 422 `invalid_fields` (`player: not_a_member` / `is_you`) |
| `room-start` | `POST /v1/rooms/{room}/start` | | 200 Room | 403 `not_member` / `host_only`, 409 `room_started` / `room_closed` / `too_few_players` |
| `room-finish` | `POST /v1/rooms/{room}/finish` | `{scores?}` | 200 Room (back in `lobby`) | 403 `not_member` / `host_only`, 409 `room_closed` / `room_not_started`, 422 `invalid_fields` (as `room-end`'s scores) |
| `room-end` | `POST /v1/rooms/{room}/end` | `{scores?}` | 200 Room | 403 `not_member` / `host_only`, 409 `room_closed`, 422 `invalid_fields` (`scores: not_playing` in the lobby, `scores.N.player: not_a_member` / `duplicate`, `scores.N.rank: must_be_positive`) |
| `room-host` | `POST /v1/rooms/{room}/host` | `{player}` | 200 Room | 403 `not_member` / `host_only`, 409 `room_closed`, 422 `invalid_fields` (`player: not_a_member`) |
| `room-ticket` | `GET /v1/rooms/{room}/ticket` | | 200 `{token, expires_in}` | 403 `not_member`, 409 `room_closed` |

Inviting friends to a room, `room-invite` (`POST /v1/rooms/{room}/invites`),
is docs/FRIENDS.md's (M6).

Every lifecycle change made over HTTP is published to the room's sockets
(the frames below). The socket itself carries no lifecycle commands: the app
starts, ends, kicks, hands over and leaves over HTTP, and hears the result
on the socket like everyone else.

### Room

```json
{
  "id": "room_7f3k9d2m8q1x4b6n",
  "code": "K7M2QX",
  "game": { "id": "fixture.xo", "name": "إكس أو", "icon": "https://…", "orientation": "portrait" },
  "status": "lobby",
  "round": 0,
  "visibility": "private",
  "lan": false,
  "host": "ply_7f3k9d2m8q1x4b6n",
  "config": { "min": 2, "max": 2, "host": "authoritative", "pace": "turn",
              "state": "blob", "rejoin": true, "spectators": false, "lan": true },
  "members": [Member],
  "you": { "role": "host", "seat": 0 },
  "state_version": 0,
  "link": "http://localhost:8080/r/K7M2QX",
  "end_reason": null,
  "results": [],
  "created_at": "2026-10-21T10:00:00Z",
  "started_at": null,
  "ended_at": null
}
```

`members` are the current members, by seat. `you` is the caller's own role
and seat (`null` for a caller who has left or been kicked: anybody who was
ever in a room can still read it). `state_version` is the live version
(the relay's) while the room is open, the copied one once it has ended.
`link` is `LINK_PUBLIC_URL/r/{code}`; the app catches it, and the page
behind it is docs/JOINING.md's (M7).
`game.icon` is a signed link, empty when the game has none. `round` is how
many rounds have started (0 in a fresh lobby). `results` are the results of
the round in `round`, [Result] (see *Results (M5)* and *Rounds*), `[]` until
there are any. `started_at` is that round's start.
Times are the API's one format, UTC to the second (`2026-10-21T10:00:00Z`),
here and in every frame's `at`.

### Member

```json
{ "id": "ply_…", "nickname": "زين", "handle": "zayn_4821", "avatar": "a03",
  "avatar_url": null, "role": "host", "seat": 0, "connected": true }
```

`connected` is whether the member has a live socket to this room (their
presence key says `in_room` with this room). `avatar` is a bundled avatar's
id and `avatar_url` null, or `avatar` empty and `avatar_url` a signed link to
an uploaded picture.

## The socket

`GET /v1/rooms/{room}/socket?after={seq}` upgraded to a WebSocket.

- **Subprotocol.** The client offers `gathering.v1`; the server selects it and
  refuses an upgrade that does not offer it (400 `subprotocol_required`).
- **Who.** The player's session token, either as `Authorization: Bearer
  <token>` (the app) or as a second offered subprotocol `bearer.<token>`
  (a browser, which cannot set headers). Resolved like any session call
  (whitelisted jti, scope `session`) **before** upgrading, so a refusal is a
  plain HTTP answer with the API's error body, in this order: 426
  `upgrade_required` (not a WebSocket upgrade), 400 `subprotocol_required`,
  401 with the session's reason as on any call (`missing_token`,
  `invalid_token`, `expired_token`, `revoked_token`), 403 `wrong_token`
  (another kind of token: a room ticket opens nothing here), 422
  `invalid_fields` (`after: must_be_a_whole_number`), 404 `not_found` (no
  such room, never a member, or a member who left), 403 `kicked`, 409
  `room_closed`. Any origin may connect: the bearer is offered explicitly on
  every connect and nothing ambient rides along.
- **One socket per member.** A second connect for the same (room, player)
  replaces the first, which is closed with 4000 — on whichever replica it
  is: each connect announces itself on the room's channel (a `_conn` notice
  the hubs read and never forward), and of two sockets for one member the
  one announced last stays.
- **`after`.** The last `seq` the client saw before it lost the connection
  (0 or absent for a first connect). See *Order and catching up*.

### Close codes

| Code | Reason text | The app |
|---|---|---|
| 4000 | `replaced` | stops: another connection took over |
| 4003 | `left` / `kicked` | stops: no longer a member |
| 4004 | `ended` | stops: the room is over |
| 4008 | `slow_consumer` | reconnects with `after` |
| 1001 | `going_away` (server shutdown, idle, or frames the replica lost) | reconnects with `after` |
| 1011 | `internal_error` (something on the server broke opening the socket) | reconnects with `after` |
| anything else, or a dropped network | | reconnects with `after`, backing off 1, 2, 4, 8, 16, then 30 s |

### Frames from the server

Frames with a `seq` are the room's history; `hello`, `replay`, `pong` and
`error` belong to one connection and carry none.

| `t` | Fields | When |
|---|---|---|
| `hello` | `seq` (the room's current seq), `room` (Room, `you` filled), `state` (`{version, data}`; `data` null until written; the whole field null when `config.state` is `none`) | first, on every connect |
| `replay` | `frames` (the history frames after `after`, in order) | right after `hello`, when `after` was given and the history still holds them |
| `joined` | `seq`, `player` (Member) | someone joined |
| `left` | `seq`, `player` (id), `reason` (`left` / `kicked`) | someone left or was kicked |
| `members` | `seq`, `list` ([Member]) | after any join, leave, kick, host change, connect or disconnect |
| `msg` | `seq`, `from`, `data`, `to?` | a member's message |
| `state` | `seq`, `version`, `data`, `by`, `ref?` | the state changed |
| `started` | `seq`, `at`, `by`, `round` | the host started a round; the state was reset with it |
| `finished` | `seq`, `at`, `by` (null for the sweeper), `round`, `reason` (`host` / `timeout`), `results` ([`{player, score, rank}`]) | a round is over and the room is back in its lobby; no socket closes |
| `ended` | `seq`, `at`, `by` (null for the sweeper, an empty room and the console), `reason` (`host` / `empty` / `idle` / `admin` / `tournament`), `round`, `results` ([`{player, score, rank}`]: the end's scores, or the game's results when its server wrote them first) | the room ended |
| `host` | `seq`, `player`, `by` (null when the old host left) | the host changed |
| `kicked` | `seq`, `player`, `by` | the host removed someone |
| `pong` | `at` | answers `ping` |
| `error` | `code`, `ref?`, `version?` | a frame of this connection's was refused |

`error` codes: `bad_frame` (not JSON, unknown `t`, missing field, a
`version` that is not a whole number, a `ref` over 128 characters),
`too_large`, `rate_limited`, `not_member` (a `to` that is not a current
member), `state_stale` (with the current `version`), `host_only` (a state
write by a player in an `authoritative` room), `no_state` (`config.state`
is `none`), `gap` (the history no longer holds `after`), `slow_consumer`
(just before the 4008 close). Every refusal carries the refused frame's
`ref` when it had one, `rate_limited` included; `too_large` never does (the
frame was not read).

A kick publishes `kicked`, then `left` (reason `kicked`), then `members`, and
closes the kicked member's socket with 4003. A leave publishes `left`, then
`members` (and `host` first when the host left), and closes the leaver's
socket with 4003. An end publishes `ended` and closes every socket of the
room with 4004 once it is delivered. The hubs act on the frames they deliver:
the replica holding a socket closes it, after the frame itself has gone out.
The last member leaving publishes `left`, then `ended` (no `members`).

`members` is also published when a socket connects and when one closes —
unless it closed because it was replaced, because its member left or was
kicked, because the room ended (each of those changes sends the list
itself), or because the server is shutting down.

### Frames from the client

| `t` | Fields | Rule |
|---|---|---|
| `msg` | `data`, `to?` | no `to`: delivered to every connected member, the sender included (games tell their own echo by `from`); `to`: delivered to that member only, not echoed |
| `state` | `version`, `data`, `ref?` | compare-and-set, below |
| `ping` | | every 20 s; refreshes presence; answered with `pong` |

`data` is any JSON value. `ref` is the client's own correlation string,
echoed on the `state` frame it produced (every member's copy) and on an
`error` it caused.

### Order and catching up

- Every history frame is published by one Redis Lua script that increments
  `room:{id}:seq`, splices the number into the frame, appends the frame to
  `room:{id}:log` (the last 200, 2 h TTL), and publishes it on the channel
  `room:{id}`, atomically. The numbers are the publish order across replicas;
  a subscriber delivers in channel order. The seq is spliced in right after
  the type, so every history frame reads `{"t": …, "seq": N, …}`.
- One hub per process holds one Redis connection subscribed to every room
  with a local socket. Frames are counted from the room's subscription
  confirmation on. A seq that skips one (frames lost on the channel), or
  Redis confirming a room's subscription a second time (go-redis
  resubscribing after a dropped connection), closes that room's local
  sockets with 1001: they reconnect with `after` and replay what was lost.
- On connect the server registers the connection with its process's hub
  (subscribing to the channel if it is the first local connection to that
  room), reads the current seq **S**, builds `hello` from the database and
  Redis, sends it, then, when `after` = **A** was given and A < S, sends
  `replay` with the logged frames A < seq ≤ S that this member may see (or
  `error{code: "gap"}` when the log no longer reaches back to A + 1), and from
  then on forwards live frames with seq > S. Live frames that arrive while
  this happens are held and delivered after, in order. A `replay` is sent
  whenever A < S and the log reaches back, even with no frames for this
  member in it; A ≥ S sends none. If hello shows the room ended, or the
  member no longer in it, the socket is closed (4004, or 4003 `left` /
  `kicked`) right after hello: the frame that would have closed it was
  published before this socket could hear it.
- `hello` may already reflect a change whose frame follows it. Every frame is
  safe to apply twice: `members` replaces the list, a `state` frame whose
  `version` is not above the client's current one is ignored, and the client
  drops any history frame whose `seq` it has already seen.

### State

- Kept in Redis as `room:{id}:ver` (integer) and `room:{id}:data` (the raw
  JSON), 2 h TTL refreshed on every write — every history frame refreshes all
  four keys of the room, and so does the once-a-minute touch on a ping, so a
  quiet room with members connected keeps them — and copied to Postgres when
  the room ends. `version` is a whole number, 0 or more; `data` any JSON
  value, `null` included.
- A `state` frame from the client carries the `version` it was based on.
  When that is the current version, the server stores `data` as version + 1
  and publishes `state{version: v + 1, data, by, ref}` (one Lua script: check,
  write, increment seq, log, publish). Otherwise it answers that connection
  `error{code: "state_stale", version: current, ref}`.
- Who may write: only the host when `config.host` is `authoritative`; any
  member when it is `peer`. `config.state` `none` refuses every write
  (`no_state`).
- Messages and state are allowed in `lobby` and `playing`. A state write
  that arrives as the room ends is dropped without an answer: the socket is
  about to close with 4004.

### Limits

- A frame from the client is at most `RELAY_MAX_FRAME` (16 KiB):
  `error{code: "too_large"}`, frame dropped, socket kept.
- `RELAY_RATE` (30) frames a second per socket, a token bucket in memory
  with a burst of the same size, every frame counted, pings included:
  `error{code: "rate_limited"}`, frame dropped.
- No frame from the client for 45 s closes the socket (1001); the app pings
  every 20 s.
- A socket whose outbound queue (256 frames) is full is closed with 4008
  after `error{code: "slow_consumer"}`.
- `config.max` never exceeds `RELAY_MAX_MEMBERS` (64; the manifest check
  already caps it).

### Presence

On connect and on every ping, the relay sets `presence:{player}` to
`{"state": "in_room", "room": id}` with `PRESENCE_TTL`; on a close that was
not a replacement it sets `online` — nor a server shutting down, whose
sockets are already reconnecting elsewhere (the key expires by itself if
they do not). `connected` in a Member reads it. A socket connecting also
moves the room's `active_at`; a ping moves it at most once a minute
(`room:{id}:touch`).

## Tickets, the launch and the bridge context

- `GET /v1/rooms/{room}/ticket` mints the room ticket: `sub` the player,
  `aud` the room's game (a one-element list, as JWT libraries write it and
  check it: `aud: ["fixture.xo"]`), `room`, `role`, `seat` (always present:
  the host's is 0), scope and kind `room`, `iss` `JWT_ISSUER`,
  `TOKEN_ROOM_TTL` (10 min). A game's server verifies it against
  `/.well-known/jwks.json` (ES256, by `kid`). Refusals: `not_member` (no such
  room, or not a current member), then `room_closed`.
- `POST /v1/games/{game}/session?room={room}` now checks the room, before
  the game is put on the shelf: a room that does not exist or that the
  caller was never in is 403 `not_member`; it must be open (409
  `room_closed`), the caller a current member (403 `not_member`), and the
  room's game this game (422 `invalid_fields`, `room: another_game`). The
  code carries the room; the game token minted from it has the `room`
  claim.
- `GET /v1/bridge/context` answers `room` for a token with a `room` claim
  whose player is still a current member: `{id, code, status, role, seat,
  members: [{id, nickname, role, seat}]}` (the current members by seat;
  `nickname` only with `identity:read`, absent otherwise). Otherwise `room`
  is null.

## Redis keys

`{id}` is written with its braces: `room:{room_7f3k9d2m8q1x4b6n}:seq`. The
braces are a Redis Cluster hash tag, so every key of one room lands in one
slot and one script may touch them all.

| Key | What | TTL |
|---|---|---|
| `room:{id}:seq` | the frame counter | 2 h, refreshed |
| `room:{id}:log` | the last 200 history frames | 2 h, refreshed |
| `room:{id}:ver`, `room:{id}:data` | the state | 2 h, refreshed |
| `room:{id}:touch` | `active_at` throttle | 60 s |
| channel `room:{id}` | the live frames, and the hubs' own `_conn` notices (never forwarded) | |
| `presence:{player}` | `{state, room}` | `PRESENCE_TTL` |

Ending a room deletes its seq, log, state and touch keys once the `ended`
frame is published.

## The app

- `RoomsApi` speaks the HTTP table. `CloudRoomTransport` opens
  `ws(s)://<api>/v1/rooms/{id}/socket?after=…` with `Authorization` and the
  `gathering.v1` subprotocol, pings every 20 s, and reconnects as the close
  codes say. Before every reconnect it reads `GET /v1/rooms/{id}` through
  the Dio client, which refreshes an expired session on the way and tells it
  to stop on a 404, on a 409, or on a 200 whose `you` is null (the player
  left or was kicked: a former member can still read the room). A refused
  upgrade (404, 403 `kicked`, 409 `room_closed`) also means stop.
- `RoomSession` (one per open room, owned by `RoomsCubit`, outliving every
  page) holds the Room, the state, the last seq and the connection state;
  sends `msg` and `state` (a pending `state` resolves when its own `ref`
  comes back, and rejects on any `error` carrying that `ref`: `state_stale`,
  `host_only`, `no_state`, `rate_limited`, `bad_frame`); and turns frames
  into the bridge's events.
- `window.gathering.room` maps onto it: `current()` answers `{id, code, game,
  status, you: {role, seat}, config, seq, startedAt}` (`startedAt` in
  milliseconds of the phone's own clock, null before the start: the moment it
  heard the start live, else the server's start moved by the clock gap a `pong`
  or `started` frame showed, so a page compares it with `Date.now()`), `members()` the
  members (with `avatarUrl`), `send(data, {to})` a `msg`, `getState()` the
  session's `{version, data}`, `setState(data, version)` a `state` frame (its
  promise resolves to the new version), `start()` / `end({scores})` /
  `leave()` / `kick(id)` the HTTP routes, `ticket()` the ticket route. A
  refusal rejects with the API's code (`host_only`, `room_started`, …).
  Events, as DOM events `gathering:room:<event>` with the frame's fields as
  `detail`: `message` (msg), `members` (the list), `joined`, `left`,
  `state`, `started`, `ended`, `host`, `kicked`, and `connection` (`{state:
  connecting | open | reconnecting | closed}`, the app's own).
- The lobby's start key starts the room over HTTP; on `started` every
  member's app opens the game with `?room=` (a game opened after the start
  syncs from `startedAt`). At a round's end the frame stays until the player
  closes it; the lobby then shows the round's results, and the host plays
  again in the same room (*Rounds*, "The app").

## Results (M5)

*Rounds* makes every rule below a rule per round: rows are per `(room,
round)`, and "the room's results" are a round's.

A room's results are kept for good, outliving the room row (deleted 24 h after
it ends). They come from one of two places: the host's end, or the game's own
server. The game's server is the stronger word: once it has written a room's
results, the host's end no longer touches them.

### Storage (migration `00008_results.sql`)

```sql
results(
  room_id varchar(24) NOT NULL,        -- no foreign key: the room row goes, the results stay
  player_id varchar(24) NOT NULL REFERENCES players(id),
  game_id varchar(64) NOT NULL REFERENCES games(id),
  code varchar(6) NOT NULL,            -- the room's code, copied
  seat integer NOT NULL,               -- the player's seat when the results were written
  score numeric NOT NULL,
  rank integer NOT NULL CHECK (rank >= 1),
  players integer NOT NULL,            -- how many were ranked in this room
  finished_at timestamptz NOT NULL,
  submitted_by text NOT NULL,          -- host | game
  PRIMARY KEY (room_id, player_id)
)
INDEX results_history ON results(player_id, finished_at DESC, room_id)
INDEX results_game ON results(game_id, player_id, finished_at DESC)   -- leaderboards, M6
```

`code` and `seat` are copied in because the room row and its members are
deleted a day after the end, and the reads below still answer the room's code
and list ties in seat order. A seat is the one the player held in
`room_members` when the results were written (a former member's last seat).
All the rows of one room come from one write: one `submitted_by`, one
`finished_at`, one `players`.

### Writing them

- **The host's end** (`POST /v1/rooms/{room}/end` with scores) writes one row
  per score, `submitted_by: host`, `finished_at` = the end, `players` = the
  number of scores, in the same transaction as the end, unless the room
  already has rows from the game. An end without scores writes none.
  `rooms.results` keeps its copy as before.
- **When the game has written first**, the end leaves its rows alone: the
  scores sent are still checked (a bad one is still refused) but not kept, and
  `rooms.results` and the `ended` frame carry the game's results instead,
  ranked, ties in seat order. Every other end does the same (the last member
  leaving, the sweeper's `timeout`), so a room has one set of results
  wherever it is read.
- **The game's server**: `POST /v1/bridge/results` (operation
  `bridge-results`, guest group, scope `game`) with a game token carrying
  `results:write` (else 403 `scope_required`, `scope: results:write`; any
  other kind of token is 403 `wrong_token`). Body `{room, scores: [{player,
  score, rank?}]}`, at most 256 scores. The token need not carry the room:
  any of the game's tokens for a player who was in it will do. Refusals, in
  this order: 404 `not_found` (no such room, or its row is gone); 422
  `invalid_fields` `room: another_game` (the room is another game's); 403
  `not_member` (the token's player was never in the room); 409
  `room_not_started` (a lobby); then 422 `invalid_fields` for the scores:
  `scores: required` (an empty list), `scores.N.player: not_a_member` (never
  in the room) / `duplicate`, `scores.N.rank: must_be_positive`. The room's
  rows are replaced whole (delete and insert in one transaction, holding the
  room's row like every transition), missing ranks computed as for an end,
  `submitted_by: game`, `finished_at` = the room's end when it has ended,
  else now. An ended room's `rooms.results` is rewritten to match (in the
  order sent). Answers 200 `{results: [Result]}`: the room's results as they
  now stand, ranked, ties in seat order, with `nickname`, `avatar` and
  `avatar_url` only with `identity:read` (absent otherwise, as everywhere on
  the bridge). A playing room keeps playing: the host still ends it, and
  nothing is published to the sockets.
- Every write appends a `logs.room_events` row: event `results`, `player_id`
  the writer (the host, or the game token's player), detail `{"by": "host" |
  "game", "scores": N}`. A host's end with scores writes `ended`, then
  `results`.

### Result

```json
{ "player": "ply_…", "nickname": "زين", "avatar": "a03", "avatar_url": null,
  "score": 1, "rank": 1, "submitted_by": "host" }
```

`avatar` and `avatar_url` follow the Member's rule: a bundled avatar's id and
null, or empty and a signed link to an uploaded picture. The Room's `results`
now come from this table (names joined, so a player who left still shows),
ranked, ties in seat order; a room with no rows that ended before M5 falls
back to `rooms.results` (`submitted_by: host`). The `ended` frame keeps its
`[{player, score, rank}]`.

### Reading them

| Operation | Method and path | Answer | Refusals |
|---|---|---|---|
| `me-history` | `GET /v1/me/history?cursor=&limit=` | 200 `{items: [HistoryItem], next_cursor}` | 422 `invalid_fields` (`limit: 1_to_50`, `cursor: invalid`) |
| `room-results` | `GET /v1/rooms/{room}/results?round=` | 200 `{room, code, game, round, rounds, finished_at, results: [Result]}` | 422 `invalid_fields` (`round: must_be_positive`), 404 `not_found` (no results row of the round shown has the caller, and the caller is not a member) |

The history is the caller's own rows, newest first (`finished_at`
descending, then room id ascending, which is the `results_history` index's
order). `limit` defaults to 20; 0, below, above 50 is `limit: 1_to_50` (a
`limit` that is not a whole number is refused by the request parser, still
422 `invalid_fields`). `next_cursor` is opaque (the last item's `finished_at`,
to the microsecond, and room id) and null on the last page; anything that is
not a cursor the API handed out is `cursor: invalid`.

`room-results` reads the results table, so it answers after the room row is
deleted. While the row exists, a current or former member may read it even
with no results yet: `results` `[]`, `finished_at` the room's end, null while
it is open. Otherwise `finished_at` is the results' own. `game` is the
Room's Game; `results` are ordered as the Room's.

```json
HistoryItem:
{ "room": "room_…", "code": "PY6LSX",
  "game": { "id": "fixture.xo", "name": "إكس أو", "icon": "https://…", "orientation": "portrait" },
  "score": 1, "rank": 1, "players": 2, "finished_at": "2026-10-21T10:02:11Z",
  "submitted_by": "host" }
```

### The partner fixture verifies a ticket

`cmd/fixtures` gains `POST /partner/verify-ticket {token}`: it checks the room
ticket's ES256 signature against `${API_URL}/.well-known/jwks.json` (cached
five minutes; a `kid` it has not seen fetches the set again, at most once
every ten seconds, so a rotated key is picked up without waiting the cache
out), `aud` = its own client id, kind `room`, and `exp`, and answers 200
`{valid: true, claims: {sub, room, role, seat, aud, exp}}` (`aud` a list, as
the ticket carries it; `exp` in seconds since the epoch) or 401 `{valid:
false, error}`, `error` one of `malformed`, `unknown_key`, `bad_signature`,
`expired`, `not_yet_valid`, `wrong_audience`, `wrong_kind`. A body without a
token is 400 `{valid: false, error: "invalid_request"}`; a JWKS it cannot
fetch is 502 `{valid: false, error: "jwks_unavailable"}`. That is the proof
that a studio's server needs no session of ours to know who sits where. The
e2e sweep drives it when `E2E_FIXTURES` is set.

### The app (M5)

- `ResultsApi`: `history({cursor, limit})`, `roomResults(roomId)`.
- `ResultsPage(roomId)`: the ranks with medals for the first three, avatars,
  names, scores, the player's own row marked, a rank reveal as it opens; for
  the host "العب مرة ثانية", which since *Rounds* starts the next round in
  the same room (it made a new room before), and the way back for
  everyone. The lobby shows it in its place after every round.
- `HistoryPage` from the Me tab ("سجل اللعب"): newest first, paged by
  `next_cursor`; a row opens that room's `ResultsPage`.
- `GaCelebrate`: the overlay behind `gathering.celebrate({kind, title})` —
  confetti in the arena's colours, the card (win · lose · draw · rank), a
  sound and a haptic; dismissing it resolves the promise. Reduced motion
  draws the card without the confetti.
- Sounds: `assets/sounds/{tap,tick,join,leave,win,lose,countdown}.wav`, made
  for this app (generated, no licence to track), played through a small
  `SoundService` that honours the muted switch (a new "الأصوات" row in the Me
  tab, `GatheringPrefsKeys.muted`); `gathering.playSound` / `setMuted` use
  it, and the lobby plays join and leave.

## Rounds (playing again in the same room)

Added 2026-10-21, after M6. Players who finish a game play the next one in the
same room: same code, same seats, same sockets, nobody rejoins. A game's end
finishes a **round** and puts the room back in its lobby; only closing it, the
last member leaving, or idling ends the room. Where an earlier section says
otherwise, this one is right.

### The room between rounds

- `round` is how many rounds have started: 0 in a fresh lobby; while
  `playing`, the round being played; back in the `lobby`, the round just
  finished. It only ever grows.
- The status goes `lobby` → `playing` → `lobby` → `playing` → … and becomes
  `ended` only when the room closes. `started_at` is the current (or last)
  round's start; `ended_at` and `end_reason` are the room's own end, as before.
- Between rounds the room is a lobby like any other: people join by the code
  (every rule of *Joining*: friends-only, full, kicked), leave, are kicked or
  invited, the host hands over, and the host starts the next round. Somebody
  who left during a round may come back in the lobby whatever `config.rejoin`
  says (rejoin only governs coming back while a round plays).

### Storage (migration `00011_rounds.sql`)

```sql
ALTER TABLE rooms ADD COLUMN round integer NOT NULL DEFAULT 0;   -- rounds started
UPDATE rooms SET round = 1 WHERE started_at IS NOT NULL OR id IN (SELECT room_id FROM results);
ALTER TABLE results ADD COLUMN round integer NOT NULL DEFAULT 1;
ALTER TABLE results DROP CONSTRAINT results_pkey;
ALTER TABLE results ADD PRIMARY KEY (room_id, round, player_id);
DROP INDEX results_history;
CREATE INDEX results_history ON results(player_id, finished_at DESC, room_id, round);
```

Every existing row of `results` becomes round 1, and so does its room, even
one that ended in its lobby with scores (M5 allowed that): a room's `round`
is never below a round it has rows for.

`rooms.results` is the copy of the latest round's results, as that round's
`finished` (or `ended`) carried them, or as the game's server rewrote them
since. Every finish and every end writes it; a start leaves it. It is what an
end between rounds carries, and what the Room's `results` show for a room
from before the table. The rows of one round come from one write: one
`submitted_by`, one `finished_at`, one `players` (how many were ranked in
that round).

### Starting a round

`POST /v1/rooms/{room}/start`, from the lobby, for the first round or the
next; the refusals are as before (`room_started` while a round plays,
`too_few_players`). It sets `status: playing`, `round: round + 1`,
`started_at` and `active_at` to now, writes the `started` event (detail
`{members, round}`), and publishes `started{seq, at, by, round}`. The relay
resets the shared state **in the same Lua step as that publish**: `ver` to 0
and `data` removed. Every round therefore begins at `{version: 0, data: null}`,
and a state write based on an earlier round's version is refused
`state_stale` with `version: 0`. (`Publisher.Start(ctx, room, Started)`: one
script that resets, then publishes like every frame.)

### Finishing a round

`POST /v1/rooms/{room}/finish {scores?}` (operation `room-finish`, host
only, scope `session`), from `playing` only.

- Refusals, in the host-only order: 403 `not_member`, 409 `room_closed`,
  403 `host_only`; then 409 `room_not_started` (the room waits in its lobby:
  there is no round to finish); then 422 `invalid_fields` for the scores,
  exactly as an end's.
- The scores are the round's results, by the rules of *Results (M5)*,
  "Writing them", read per round: the rows are `(room, round)`'s; when the
  game's server has already written this round's results, they stand, the
  scores sent are checked but not kept, and the finish carries the game's.
- It sets `status: lobby`, moves `active_at` and writes `rooms.results`
  (the round's results, as `finished` carries them). The code, the members,
  the host and the sockets stay; the relay keeps its keys (the finished
  round's state stays readable until the next start resets it).
- It writes the event `finished` (detail `{round, reason: "host", scores:
  N}`, N the results it carries), then `results` when it wrote rows. Every
  `results` event's detail gains `round`.
- It publishes `finished{seq, at, by, round, reason, results}`: `reason`
  `host` (or `timeout` from the sweeper, with `by` null), `results` the
  round's as `[{player, score, rank}]`, ranked, ties in seat order when they
  are the game's. No socket closes.
- It answers 200 Room: `status` `lobby`, the same `round`, `results` the
  round's.

### Ending, during a round or between them

`POST /v1/rooms/{room}/end` closes the room for good, from the lobby or
during a round:

- During a round it also finishes that round: its scores (optional) are the
  round's results by the same rules, and `ended` carries them. It writes
  `ended`, then `results` when it wrote rows, and publishes `ended` alone:
  no `finished`, in the log or on the sockets.
- In the lobby it takes no scores: a non-empty `scores` is 422
  `invalid_fields` with `scores: not_playing` (an empty list is no scores).
  `ended` then carries the latest round's results, the room's copy in
  `rooms.results`, or `[]` when no round was played.
- The last member leaving and the sweeper's idle end do the same: a round
  under way is finished by the end (its results the game's, if its server
  wrote this round's, else none), and in the lobby the end carries the
  latest round's.
- `ended` gains `round` (the latest round started, 0 when none), and the
  `ended` event's detail gains `round`.

### The sweeper

A round whose `started_at` is older than `ROOM_MAX_PLAY` is **finished**, not
ended: reason `timeout`, `by` null, results the game's when its server wrote
this round's, else none; the room goes back to its lobby. Like the host's
finish it moves `active_at` and writes `rooms.results`, and its event is
`finished` with `player_id` empty and detail `{round, reason: "timeout",
scores: N}`. The lobby rule then applies, counted from the finish: with
nobody connected for `ROOM_LOBBY_IDLE`, the room ends (`idle`), carrying the
latest round's results. `Swept.Timeout` counts the rounds it finished. Each
finish re-checks the room under its row lock, like every sweep.

### Results per round

- **Writing.** The host's finish, or an end during a round, writes that
  round's rows unless the game's server wrote that round's first.
- **The game's server**: `POST /v1/bridge/results {room, round?, scores}`.
  `round` defaults to the room's `round` (the one playing, or the one just
  finished while the room waits); it must be a whole number from 1 to the
  room's `round` (else 422 `invalid_fields`, `round: unknown`). A room that
  never started (round 0) is 409 `room_not_started`; a lobby between rounds
  is fine. The other refusals and their order are M5's; `round: unknown`
  comes after `room_not_started`, in the same 422 as the scores' problems
  (every problem at once). That round's rows are replaced whole.
  `finished_at` is the round's rows' own when it already has some; else the
  room's end, when the round was its last and the room has ended; else now.
  When the round is the room's latest and no longer playing, `rooms.results`
  is rewritten to match. A round that is playing keeps playing, and nothing
  is published. The answer is 200 `{round, results}`: the round written, and
  its results as they now stand.
- **The Room's `results`** are the rows of round `round`: empty while that
  round plays, unless its game wrote them early. A room with no rows for any
  round (one that ended before M5) falls back to `rooms.results`, as before.
- **`GET /v1/rooms/{room}/results?round=N`**: A `round` that is not a whole
  number from 1 is 422 `invalid_fields`, `round: must_be_positive`. Access is
  M5's rule, per round: a caller who is or was a member of the room may read
  every round while its row exists; anybody else only the rounds they have a
  row in; otherwise 404 `not_found`. `round` defaults to the latest round
  with rows that the caller may read, else (a member) the room's `round`. A
  round with no rows answers `results: []` and `finished_at` null for a
  member; a room with no rows for any round reads its copy for its latest
  round, `finished_at` its end, as in M5. The answer gains `round` (the round
  shown) and `rounds` (every round of this room that has rows and that the
  caller may read, ascending).
- **`GET /v1/me/history`**: one item per round the player has a row in.
  `HistoryItem` gains `round`. Order: `finished_at` descending, then `room`
  ascending, then `round` ascending. The cursor carries the round too (the
  last item's `finished_at` to the microsecond, its room and its round); a
  cursor written before rounds is refused `cursor: invalid` (a client holds a
  cursor for a scroll, not for good).
- The friends' leaderboard (docs/FRIENDS.md) counts rows, so every round is a
  play; it does not change.

### Tickets and the bridge context

- The room ticket gains the claim `round`: the round when it was minted (0 in
  a lobby that never started), always present, like `seat`. The fixtures'
  `POST /partner/verify-ticket` answers it among the `claims`.
- `GET /v1/bridge/context` answers `round` in its `room`.

### The app

- **Models.** Room `round`; RoomResults `round` and `rounds`; HistoryItem
  `round`.
- **Progress, not status.** `RoomSession` orders what it learns about a room
  by progress: `ended` above everything; otherwise `2 × round − 1` while
  playing and `2 × round` in the lobby (0 in a fresh lobby). `hello`,
  `absorb` and every route's answer move the session forward by progress
  only, so a late answer never takes a finished round back to playing, nor a
  new round back to the lobby.
- **`started`** with a round above the session's: the session's state resets
  to `{version: 0, data: null}` (the relay reset it), the start is heard as
  before (`pageStartedAt`), and the page hears `started` with `round`.
- **`finished`**: the room is in its lobby again, with the round's results.
  The page hears **`ended`** for it, once per round, with `{at, by, reason,
  round, results}`. To a game, its game is over; the room's own life is the
  app's business. An `ended` frame (the room closed) during a round the page
  has not yet heard end gives the page that round's `ended`; between rounds
  it gives the page nothing.
- **The bridge.** `room.current()` gains `round`. `room.end({scores})` is
  `POST …/finish`: the round, not the room. `room.start()` starts the next
  round from the lobby. One page load is one round: when the next round
  starts, the app opens the game again, fresh, and the state is back at
  version 0. GAME_BRIDGE.md and the docs' Bridge and Rooms pages say so.
- **A page from a finished round** that is still open when the next round
  starts is replaced by a fresh page for the new round, on every phone.
- **After a round**, the lobby shows the round's results in its own place:
  the Results page with the live session and that round (the standings and
  the reveal, the round's number). The host's "العب مرة ثانية" starts the
  next round in this room, and every member's game opens as on any start.
  Everybody has a way back to the room's lobby view (code, seats, invite,
  leave) without leaving the room, and whoever is not the host is told that
  the host starts the next round. The results stay until the player goes
  back to the lobby view or the next round starts. The header's back goes to
  the hub with the room kept, and the Play tab's room card brings the player
  back, as from the lobby.
- **From the History**, the Results page shows that item's round.
- Playing again never makes a room: nothing in the app calls `POST /v1/rooms`
  to play again.

### Proofs

- Platform: the rules' tests (a start resets the state and counts the
  round; finish only from a round, with its refusals; a round's results by
  the host or the game, the game's first per round; an end during a round
  finishes it; an end in the lobby refuses scores; the sweeper finishes a
  long round and then ends the idle lobby; history per round and its cursor;
  `room-results` with `round` and `rounds`; the bridge's `round` by default,
  given, and unknown; the ticket's `round`), the e2e rooms sweep playing two
  rounds in one room, and `cmd/roomplayer -rounds N`.
- App: the session's progress, `finished`, the reset on `started`, the
  bridge's `ended` once per round, the page replaced on a new round, the
  results in the lobby's place, "العب مرة ثانية" calling `start` on the same
  room. On devices, `scripts/room_e2e.sh` plays two rounds in one room by
  default (`E2E_ROUNDS`), and the friends test invites the guest back into
  the same room.

## Rooms from inside a game (2026-10-22)

A player who opened a game on its own (from the store, outside any room) can
make a room for it, or join one, without leaving the game's screen: the game
asks through the bridge, or the player uses the frame's own menu. Where an
earlier section says otherwise, this one is right.

### The platform

- `POST /v1/rooms/join {code, game?}`: `game`, when given, is the game the
  caller expects. When the room holding the code is another game's, the join
  is refused with **409 `another_game`** and nothing changes. The check comes
  right after the code is found (`code: must_be_6_symbols`,
  `room_not_found` and `room_closed` keep their place before it), so it
  holds even for somebody who is already in that room. Without `game`
  nothing changes.
- Making a room is already `POST /v1/rooms {game, visibility?}`.

### The bridge (`window.gathering.room`, on a page outside a room)

- `room.create({visibility?})` answers `{id, code}`. The app makes a room
  for the page's own game (`visibility` `private`, the default, or
  `friends`) and the room's **lobby takes the page's place**: the frame
  closes and the lobby opens, where the host shares the code (the QR, the
  link, the invite sheet) and starts. When the round starts, the game opens
  again, in the room, as on any start.
- `room.join({code?})` answers `{id, code}`.
  - With `code`: the app joins it as if it were typed, for this game only
    (`POST /v1/rooms/join {code, game}`).
  - Without: the app opens its **Join sheet over the page, for this game
    only**: its nearby list shows this game's rooms, and a code for another
    game is refused in words. The promise settles when the player is in, or
    rejects `cancelled` when the sheet is closed.
  - Once in, the room's lobby (or its game, when a round is under way and
    the player may come in) takes the page's place.
- Refusals reject with the bare code:
  - `in_room`: the page is itself in a room. These two are for a page
    outside one (`room.current()` null).
  - `not_a_room_game`: the game's manifest has no `room` block.
  - `invalid_code`: a code that cannot be one.
  - The API's own codes as they come: `in_another_room` (the app also
    offers the way back to that room, as everywhere), `room_not_found`,
    `room_closed`, `another_game`, `room_full`, `room_started`,
    `friends_only`, `kicked`, `not_found` (the game is not published).
- Neither asks the player first. The call follows the player's own tap in
  the game, and what follows is on screen at once (the lobby), where
  leaving is one tap. A page should not count on running after its promise
  settles: the frame closes right after.

### The frame's own menu

The game frame's menu gains two entries on a page outside a room whose game
has a `room` block: "سوّي غرفة" (`room.create()` as above, private) and
"انضم لغرفة" (the Join sheet for this game). Every multiplayer game gets
them without a line of its own.

### The sample games

إكس أو and سباق النقر (`gathering_games/xo`, `gathering_games/tap`), opened
outside a room, show "العب ويا صديق" (create) and "انضم لغرفة" (join through
the Join sheet) on their first screen. The fixtures serve them.

### Proofs

- Platform: a join with the matching game, with another game's code (409
  `another_game`, nobody joined, not even a member), and without `game`
  (unchanged); an e2e step.
- App:
  - the two bridge calls, their refusals, and the page replaced by the
    lobby;
  - the Join sheet for one game: the nearby list filtered, another game's
    code refused;
  - the menu's two entries, shown only outside a room and only for a game
    with a `room` block.
- Device (`E2E_TEST=ingame`, two Android emulators):
  1. The host opens إكس أو from the store, outside any room, and taps "العب
     ويا صديق" inside the game. The lobby takes the game's place.
  2. The guest opens إكس أو from the store, taps "انضم لغرفة" inside the
     game, types the code in the Join sheet and is in.
  3. The host starts, and both play the round to its results.

## Match rooms (tournaments, 2026-10-22)

A room made to play a tournament's match (`rooms.match_id`) follows
docs/TOURNAMENTS.md, "Match rooms", which amends every section above for
it: only the match's players come in (`403 not_in_match`, in the join's
order right after `kicked`, in place of the friends door); `room-kick` and
`room-invite` refuse `409 tournament_room`; every round that closes with
results is counted for the match in the same transaction; and the round
that decides the match closes the room as an **end** with the reason
**`tournament`** in place of a finish (`ended{reason: "tournament"}`, every
socket closed with 4004). A match decided any other way ends its open room
with the same reason, nobody named. `tournament` joins the end reasons
everywhere they are listed (the Room's `end_reason`, the `ended` frame, the
console). The Room gains `tournament` (the match and its tally, null for
any other room), and so does the bridge context's `room`.

## Not yet

Spectators, the LAN relay (Phase 2), a running tally across a room's
rounds (who has won how many). The invite link's page, app links, QR codes
and nearby rooms are M7's: docs/JOINING.md. The console's rooms pages and
its close arrived with M8: docs/CONSOLE.md. Friends-only rooms, invites, friends on the bridge, the friends'
leaderboards and the "add friend" key on the results arrived with M6:
docs/FRIENDS.md.
