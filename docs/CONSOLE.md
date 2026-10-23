# The console's last pages (M8)

The contract for what M8 adds to the admin console (`web/console`) and the
routes under it (`/v1/admin/*`, admin scope, the HttpOnly console cookie),
and for the two things the app must understand because of them: a room
**closed from the console**, and an **announcement** in the inbox. The
platform builds the routes and the pages; the app builds its two parts.
When they disagree this file decides; when it is wrong it is fixed here
first.

Every route here follows the console's existing rules: the admin session,
the per-operation metadata (`authn.ScopeKey` `admin` on every route, and
`authn.RoleKey` `admin` on every change, as the registry's and the players'
changes have it: an `operator` reads; `authn.PermKey` is a game token's
permission and opens nothing here), the audit log for every change, the
API's one error body. An audit line is written as every audit line is: on
the log's own pool, after the change has committed and before the answer;
a failed write is logged, never refused. Names below are operation ids.

## Rooms

### `admin-rooms`: `GET /v1/admin/rooms?status=&game=&cursor=&limit=`

The rooms, newest first (`created_at` descending, then id), a page at a
time (`next_cursor` opaque, null on the last page; `limit` 1 to 100,
default 50).

- `status`: `open` (the default: `lobby` and `playing`), `lobby`,
  `playing`, `ended`, or `all`. `game`: one game's rooms.
- An item: `{id, code, game: {id, name, icon}, status, round, visibility,
  host: {id, nickname, handle}, members, connected, created_at,
  started_at, ended_at, end_reason}`. `members` counts the current
  members; `connected` those with a live socket (presence, as a Member's
  `connected`).
- The answer also carries `live: {lobby, playing}`, the open rooms by
  status across the platform, whatever the filters.
- The answer is `{items: [Item], next_cursor, live}`. Refusals, all at once
  as 422 `invalid_fields`: `status: unknown`, `limit: 1_to_100`, `cursor:
  invalid` (anything that is not a cursor this route handed out). A `game`
  with no rooms is an empty page, not a refusal.

### `admin-room`: `GET /v1/admin/rooms/{room}`

One room for the console: the list item's fields, plus `members` as the
list (`{id, nickname, handle, role, seat, connected, joined_at}`, by
seat), `config` (the room block), `results` (the latest round's, as the
Room has them) and `events` (its last 50 `logs.room_events`, newest first:
`{at, event, player, detail}`, `player` the player's id, null where nobody
was: the clock, an empty room, the console). 404 `not_found` for no such
room (a room deleted a day after its end is gone from here; its results are
not).

### `admin-room-close`: `POST /v1/admin/rooms/{room}/close`

Closes an open room for good, as if its host had ended it, with one
difference: the reason is **`admin`**, and nobody is named.

- 404 `not_found` (no such room), 409 `room_closed` (it has ended).
- A round under way is finished by the close, as every end does
  (docs/ROOMS.md "Rounds": its results are the game's when its server
  wrote this round's, else none; the console sends no scores).
- `end_reason: "admin"`; the `ended` frame carries `by: null`, `reason:
  "admin"`, `round`, `results`; every socket closes with 4004, as on any
  end. The room event is `ended`, with nobody as its player and detail
  `{reason: "admin", admin: <admin id>, round}` (`round` as every `ended`
  event carries it since rounds); the audit log gains `room.closed` in the
  `rooms` area, subject the room's id, detail `{code, game, round}`.
- Answers 200 with the room as `admin-room` shows it.

`admin` joins the end reasons (`host`, `empty`, `idle`, `timeout`) in
docs/ROOMS.md, the Room's `end_reason` enum and the console's words.

## Announcements

An announcement is a notification from the platform to many players at
once: kind **`system`** (reserved in docs/FRIENDS.md since M6), no actor,
no game of its own (so no game's rate limit counts it).

### Storage (migration `00012_announcements.sql`)

```sql
announcements(
  id varchar(24) PRIMARY KEY,          -- ann_ + 16
  admin_id varchar(24) NOT NULL REFERENCES admins(id),
  audience jsonb NOT NULL,             -- as sent: {kind, players?, game?}
  title text NOT NULL,
  body text NOT NULL DEFAULT '',
  game_id varchar(64) REFERENCES games(id),   -- the game the row opens, if any
  reach integer NOT NULL,              -- how many players it was written for
  created_at timestamptz NOT NULL
)
INDEX announcements_recent ON announcements(created_at DESC, id)
```

### `admin-notify`: `POST /v1/admin/notifications`

Body `{audience, title, body?, game?, dry_run?}`.

- `audience` is one of:
  - `{kind: "all"}`: every player who is not disabled;
  - `{kind: "game", game: <id>}`: every player, not disabled, who has the
    game in their library (has opened it);
  - `{kind: "players", players: [<id or @handle>, …]}`: those players, at
    most 500, unknown or disabled ones skipped. A ref shaped like a player
    id is read as one; anything else is a handle, its `@` optional and its
    case ignored. A player named twice (by id and by handle) is written
    once.
- `title` 1 to 60 characters, `body` at most 240 (both trimmed, counted in
  characters), `game` an optional game id the row opens (its store page in
  the app; the game must be registered, in any listing).
- Refusals, all at once as 422 `invalid_fields`: `title: required` /
  `too_long`, `body: too_long`, `audience: required` (no audience, or one
  with no kind), `audience.kind: unknown`, `audience.game: not_found` (none
  named, or no such game), `audience.players: required` / `up_to_500`,
  `game: not_found`. A `players` audience that resolves to nobody is
  `audience.players: nobody`. An `all` or `game` audience that reaches
  nobody is not refused: the console's confirmation shows the 0 first.
- `dry_run: true` writes nothing and answers 200 `{reach: N}`: the number
  the send would reach, for the console's confirmation. It refuses what a
  send would refuse.
- Otherwise it writes, in one transaction, the `announcements` row and one
  `notifications` row per recipient (`kind: "system"`, `title`, `body`,
  `deep_link: ""`, `data: {announcement: <id>, game?: <id>}`, `actor`
  null, `game_id` null, key `announcement:<id>`); then the audit row
  `notification.sent` in the `players` area (subject the announcement id,
  detail `{audience: <kind>, reach}`), and answers 201 `{id, reach}`.
  `reach` is the rows written, which may differ from a dry run's a moment
  before. Once committed, each row is pushed to its recipient's phones
  (docs/PUSH.md).

### `admin-announcements`: `GET /v1/admin/notifications?cursor=&limit=`

The announcements sent, newest first (`created_at` descending, then id), a
page at a time: `{items: [Announcement], next_cursor}`, `next_cursor`
opaque and null on the last page, `limit` 1 to 100, default 50 (422
`invalid_fields`: `limit: 1_to_100`, `cursor: invalid`). An Announcement is
`{id, title, body, game: {id, name} | null, audience, reach, admin: {id,
name}, created_at}`, `audience` as it was sent.

## The dashboard and the players

- `admin-dashboard` gains `rooms: {lobby, playing, today}` (open rooms by
  status; rooms made since midnight UTC) and `rounds_today` (rounds
  started since midnight UTC, counted from the `started` room events, which
  outlive the rooms), beside what it answers already (`rooms_live` stays:
  `lobby` + `playing`). The first page draws them with the players and
  games it shows now.
- A player in the console (`GET /v1/admin/players/{player}`) gains
  `friends` (accepted friendships), `rounds` (results rows: rounds
  played) and `room` (the open room they are in, `{id, code}`, or null).

## The console's pages

Restyled as the console already is (internal; neutral). Every page reads
and writes through the routes above, says every refusal in words, and
works at a laptop's width and at a phone's.

- **Rooms** (`/rooms`): the live counts at the top; the list with filters
  (status, game); a row opens the room: its members and who is connected,
  its config, the latest round's results, its recent events, and
  "Close the room" behind a confirmation that says the players will be
  sent out.
- **Notifications** (`/notifications`): a form (audience: everyone, a
  game's players, or chosen players by @handle; title; body; an optional
  game to open), a preview of the row as the app draws it, a confirmation
  that names the reach (from `dry_run`) before sending; under it, the
  announcements sent.
- **Dashboard** and **Players** show the new numbers.
- **Tournaments** (`/tournaments`, 2026-10-22, docs/TOURNAMENTS.md "The
  console"): every tournament with filters (status, game); "New official
  tournament" (public, no organizer, an optional start time); a row opens
  the tournament: its entrants, its bracket by stage or its league's table,
  and, behind confirmations, start, cancel, finish (a league) and award (a
  ready match). Its audit area is `tournaments`.

## The app

- **Closed from the console.** A room that ends with reason `admin` sends
  the player back to the hub with its own notice: "سكّرت الإدارة الغرفة"
  (English: "The room was closed by Gathering"), like the other ways the
  platform closes a room. The results, if the round had any, are kept as
  for any end.
- **Announcements in the inbox.** A `system` row draws with the platform's
  mark instead of a player's avatar or a game's icon, the title and the
  body in full (up to the body's 240 characters, wrapped), and its time.
  Tapping it marks it read and, when `data.game` names a game, opens that
  game's store page; otherwise it only marks it read. A game the store no
  longer lists is a quiet toast ("هاللعبة ما موجودة هسه").

## Proofs

- Platform: the rules' tests (the list's filters, paging and counts; the
  detail; a close of a lobby and of a round under way, the `ended` frame
  and the sockets closing, the refusals; every audience, the limits, the
  dry run, the write and its audit row, disabled players skipped; the
  dashboard's and the player's new numbers), the e2e sweep (a console
  close seen by a member's socket as `ended` reason `admin`; an
  announcement to chosen players landing in their inbox), and screenshots
  of the new pages from a headless browser.
- App: the `admin` notice, a `system` row drawn and followed (with and
  without a game), their words in both languages.
