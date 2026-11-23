# Publishers: making, testing and shipping a game

The contract for the studios that make games for گعدة. A **publisher** is a
studio's account in the console. It registers its games, tries a version on
real phones with the **testers** it invites by code, sends that version to
**review**, and publishes it once an admin approved it. The platform
(`backend/internal/biz/games`, `biz/identity/publishers.go`, the publisher,
admin and player routes), the console (`web/console`) and the app
(`gathering_app`) all build to it. When they disagree this file decides, and
when it is wrong it is fixed here first.

A game is still a document we read (the manifest, `docs.ga3da.fun`'s "Getting
listed"): a studio hosts its game and its manifest itself, and gives us the
address. Nothing here uploads or hosts a game's files.

Games made before publishers existed, ours included, have no publisher and are
run from the console's Games pages as before. Everything below is for a game a
publisher registered.

## Who is who

- **An admin** creates publisher accounts, reviews builds, and keeps every
  switch the Games pages had (suspend, feature, the store copy of a live game).
- **A publisher** is one sign-in (an email and a password) for one studio. It
  sees its own games only: any other game is `404 not_found` to it.
- **A tester** is a player of the app who typed one of a game's invite codes.
  A tester plays the version under test instead of the live one.

## The pieces

**A build** (`bld_…`) is one version of a game on its way to players: a
manifest read from an address the publisher gave, and the store copy (tagline,
description, category, icon, screenshots) it will have once published, plus
the publisher's notes for testers and reviewers.

A game has **at most one open build** at a time. Its statuses:

| Status | Open | Meaning |
| --- | --- | --- |
| `testing` | yes | Being tried. The publisher may change anything. |
| `in_review` | yes | Sent to review. Frozen until an admin decides or the publisher takes it back. |
| `approved` | yes | An admin approved it. The publisher publishes it when ready. |
| `rejected` | yes | An admin sent it back with a note. The publisher may change anything and send it again. |
| `published` | no | It is the live game now. |
| `withdrawn` | no | The publisher discarded it. |

Any change to an `approved` build (a re-read whose document differs, the store
copy, a picture) takes it back to `testing`: the approval was for what the
reviewer saw. A change to a `rejected` build keeps it `rejected`, with its note,
until it is sent again.

**The live game** is the game's row, as before: what every player who is not a
tester gets, once it has been published. A build only reaches it by being
published. A publisher's game is never re-read by the daily round, so its live
manifest changes only through a published build (an admin's "Re-read it now"
on the game's page still works, and is the admin's decision).

**Testers** are kept per game, not per build: a player who joined stays a
tester of the game, build after build, until they leave or the publisher
removes them. At most **500** testers per game.

**Invite codes** are eight symbols from the room codes' alphabet
(`ROOM_CODE_ALPHABET`), typed however they were read out: case, spaces and
dashes do not count. A code has an optional label (up to 60 characters), an
optional limit on how many may join with it (1 to 1000; none by default), and
lasts 1 to 90 days (30 by default). A publisher keeps at most 20 live codes
per game (`invites: too_many`). Revoking one stops it at once; who already
joined stays.

## What a tester gets

The version of a game a player gets is decided in one place
(`games.Catalogue`), the same for the store, the store page, the launch and a
new room:

1. A **tester** of a game that has an **open build** gets that build: its
   name, version, entry, orientation, splash, scopes, permissions, room block
   and store copy laid over the game's row (an empty icon or no screenshots
   falls back to the live game's).
2. Anybody else gets the **live game**, when it is published.
3. Otherwise the game is `404 not_found`, as an unpublished game always was.

A suspended game is nobody's: testers included.

The store row and the store page say `testing: true` when what they show is a
build; the page also carries `test_notes`, the publisher's notes. A first open
puts a tested game on the player's shelf like any other.

### Rooms

A room plays the version its host got when it was made. A room made by a
tester of a game with an open build **is a test room** (`rooms.build_id`, the
Room's `testing: true`): its room block is the build's, and only testers of
the game come in (`403 testers_only`, checked right after `kicked`). Opening
the game into a room opens the room's version, whatever the player would get
outside it: a tester in a live room plays the live game, and a test room keeps
its build after the build is published or withdrawn. A player who is no longer
a tester cannot open a test room's game (`403 testers_only`). Inviting a
player who is not a tester into a test room is refused for them
(`players.N: not_testers`, after `already_member` and `not_friends`).

Tournaments are made for live games only, as before.

### What the game's server sees

Nothing new. A token minted for a tester carries the build's scopes; the
client id and secret are the game's, whichever version is open. The bridge's
context does not say a version is under test: a game that needs to know reads
its own version, which it set.

## The player's routes (scope `session`)

### `POST /v1/tests` (operation `test-join`)

`{ "code": "K7M2-QX4P" }`. **200** `{ game: {id, name, icon}, version,
testing, already }`: `version` is the build's while one is open (`testing`
true), else the live one's; `already` says the player was a tester already
(nothing changed, and the code's count did not move).

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 422 | `invalid_fields` | `code: must_be_8_symbols` |
| 429 | `too_many_requests` | more than 20 tries an hour from one player (`retry_after`) |
| 404 | `invite_not_found` | no such code, revoked, expired, used up, or its game suspended |
| 409 | `testers_full` | the game has 500 testers |

Audit `tester.joined` (area `games`): actor the player, subject the game,
`{code}`.

### `GET /v1/me/tests` (operation `my-tests`)

Every game the player tests, newest first: `{ items: [{ game: {id, name,
icon}, publisher, version, testing, joined_at }] }`. `testing` and `version` as
above; a game with no open build and nothing live is listed with `testing`
false and an empty `version` (nothing to play until the next build). Suspended
games are left out.

### `DELETE /v1/me/tests/{game}` (operation `test-leave`)

**204**. `404 not_found` when the player was not a tester. They get the live
game from then on; nothing on their shelf is removed. Audit `tester.left`.

### Changes to existing routes

- `GET /v1/games` and `GET /v1/games/{game}`: a tester's rows as above, and
  `testing`; the page's `test_notes`.
- `POST /v1/rooms` makes a test room for a tester (above); `POST
  /v1/rooms/join` refuses `403 testers_only`; `POST /v1/rooms/{room}/invites`
  refuses `422 invalid_fields` `players.N: not_testers` when an invitee of a
  test room is not a tester; the Room carries `testing`, and a test room's
  `game` is the build's name and tile.
- `POST /v1/games/{game}/session?room=` opens the room's version; `403
  testers_only` for a test room's game opened by somebody no longer a tester.
- `DELETE /v1/me` deletes the player's testers rows.

## The publisher's routes (scope `publisher`)

The console's session for a publisher is the same cookie an admin's travels in,
holding a token of its own kind (`publisher`): an admin route refuses it `403
wrong_token`, and a publisher route refuses an admin's the same way. It ends
on sign-out, after `ADMIN_IDLE` unused, when the browser closes, at
`ADMIN_TTL`, and when an admin switches the publisher off. Only the console's
origins may open one (`403 bad_origin`).

### Signing in

- `POST /v1/publisher/session` (`publisher-session-open`) `{email, password}`.
  Refused, in this order: `403 bad_origin`; `422 invalid_fields` (`email:
  required`, `password: required`); `429 too_many_requests` after 10 tries in
  15 minutes for one email; `401 wrong_password` for an unknown email, a wrong
  password, or an account whose password was never set; `403
  publisher_disabled` once the password matched an account switched off. Sets
  the cookie; answers `{expires_at, publisher}`.
- `POST /v1/publisher/setup` (`publisher-setup`) `{token, password}`: the one-time
  link an admin made, choosing the password, and signed in. `403 bad_origin`;
  `422` `password: too_short` (under 10 characters) or `too_long` (over 72
  bytes); `401 invalid_setup` for a token unknown, used or past its time;
  `403 publisher_disabled`. A link lasts 7 days and works once; making a new
  one replaces it, and the password stays what it was until a link is used.
- `POST /v1/publisher/password` (`publisher-password`) `{current, password}`:
  `401 wrong_password`, the same `422`s. Other sessions stay.
- `DELETE /v1/publisher/session` (`publisher-session-close`): **204**.
- `GET /v1/publisher/me` (`publisher-me`): `{id, name, email}`.

### Games

- `GET /v1/publisher/games` (`publisher-games`): `{ items: [game] }`, newest
  first.
- `POST /v1/publisher/games` (`publisher-game-create`) `{url}`: reads the
  manifest as the registry does (every problem at once, `422 invalid_fields`),
  refuses an id already taken (`409 already_registered`), and makes the game,
  unpublished, with its first build in `testing`. The tier is `partner`
  whatever the document says, and the publisher's name on the game is the
  account's. **201** `{ game, client_secret }`: the secret is shown once, as
  ever.
- `GET /v1/publisher/games/{game}` (`publisher-game`): the game.
- `POST /v1/publisher/games/{game}/secret` (`publisher-game-secret`): a new
  client secret, once.

A game, as a publisher reads it: `{ id, name, listing, version, icon, tagline,
category, players, testers, created_at, build, builds }`: `version`, `icon` and
`tagline` are the live game's (empty before the first publish), `players` how
many shelves hold it, `testers` how many testers it has, `build` the open
build in full or null, `builds` every build, newest first, each `{id, version,
status, created_at, submitted_at, reviewed_at, published_at, note}`.

### Builds

- `POST /v1/publisher/games/{game}/builds` (`publisher-build-create`) `{url}`:
  a new build, read from `url` (the live manifest's address when empty), its
  store copy copied from the live game's. `409 build_open` while one is open;
  `409 suspended`; `422` `id: another_game` when the document names another
  game, `version: same_as_live` when its version is the live one's.
- `POST /v1/publisher/builds/{build}/refresh` (`publisher-build-refresh`)
  `{url?}`: reads the build's document again, from `url` when given (the
  build moves there). The same `422`s.
- `PATCH /v1/publisher/builds/{build}` (`publisher-build-edit`) `{tagline?,
  description?, category?, notes?}`: `tagline: too_long` (160),
  `description: too_long` (4000), `category: unknown`, `notes: too_long`
  (2000).
- `PUT /v1/publisher/builds/{build}/icon`, `POST
  /v1/publisher/builds/{build}/screenshots`, `DELETE
  /v1/publisher/builds/{build}/screenshots/{shot}`: as the admin's pictures
  (PNG or JPEG, 2 MB, six screenshots). A build's own pictures live under its
  own prefix; a screenshot it inherited from the live game is taken off the
  build without deleting the live game's file.
- `POST /v1/publisher/builds/{build}/submit` (`publisher-build-submit`): to
  review. `422 incomplete` `missing: [tagline, icon]` as a publish always was.
- `POST /v1/publisher/builds/{build}/cancel` (`publisher-build-cancel`): back
  from review to `testing`.
- `POST /v1/publisher/builds/{build}/publish` (`publisher-build-publish`):
  an approved build becomes the live game (below). `409 suspended`.
- `POST /v1/publisher/builds/{build}/withdraw` (`publisher-build-withdraw`):
  discards an open build; testers get the live game again.

Every action on a build checks its status first and refuses with **409**:
`in_review` (a change to a build in review: take it back first), `not_open`
(the build is published or withdrawn), `not_submittable` (submit from
`in_review` or `approved`), `not_in_review` (cancel), `not_approved`
(publish).

A build, as it is read: `{ id, game, status, version, name, manifest_url,
entry, fetched_at, orientation, splash, scopes, permissions, redirect_uris,
room, store: {tagline, description, category, icon, screenshots: [{id,
url}]}, notes, review: {note, at, by} | null, changes, new_scopes,
created_at, submitted_at, published_at, closed_at }`. `changes` names what
differs from the live game (`name`, `entry`, `scopes`, `permissions`, `room`,
`orientation`, `splash`, `tagline`, `description`, `category`, `icon`,
`screenshots`); every field differs from a game never published, so it is
empty then. `new_scopes` are the scopes the live game did not ask for.

### Publishing

In one transaction: the build's manifest becomes the game's (as an accepted
held manifest does, a held one dropped), its store copy the game's, the
account's name the game's publisher, the game `published` if it was not, the
build `published`. The live game's own pictures that the build no longer uses
are deleted from the bucket after the commit. Every player's next launch and
every new room get the new version; rooms already open keep theirs.

### Testers

- `GET /v1/publisher/games/{game}/testers` (`publisher-testers`): `{ invites:
  [{code, label, uses, max_uses, expires_at, revoked_at, created_at}],
  testers: [{id, nickname, handle, avatar, joined_at, code}] }`, newest first.
- `POST /v1/publisher/games/{game}/invites` (`publisher-invite-create`)
  `{label?, max_uses?, days?}`: **201** the code. `422` `label: too_long`,
  `max_uses: 1_to_1000`, `days: 1_to_90`, `invites: too_many`.
- `DELETE /v1/publisher/games/{game}/invites/{code}` (`publisher-invite-revoke`):
  **204**.
- `DELETE /v1/publisher/games/{game}/testers/{player}` (`publisher-tester-remove`):
  **204**; `404 not_found`. A game never published also loses its tokens for
  that player at once.

Audit lines (area `games`, actor the publisher's `pub_…` id, subject the game):
`game.registered` (with `publisher`), `build.created`, `build.refreshed`,
`build.edited` (`{fields}`), `build.submitted`, `build.cancelled`,
`build.published`, `build.withdrawn`, `game.secret_rolled`, `invite.created`,
`invite.revoked`, `tester.removed`.

## The admin's routes (scope `admin`; a change needs role `admin`)

### Publishers

- `GET /v1/admin/publishers` (`admin-publishers`): `{ items: [publisher] }`,
  newest first.
- `POST /v1/admin/publishers` (`admin-publisher-create`) `{name, email}`:
  **201** `{ publisher, setup: {token, expires_at} }`. `422` `name: required |
  too_long` (120), `email: required | invalid | too_long`; `409 email_taken`.
  The console turns the token into its own link, `/setup#<token>`, for the
  admin to send: there is no email from here.
- `GET /v1/admin/publishers/{publisher}` (`admin-publisher`): `{ publisher,
  games: [{id, name, icon, listing, version, build_status}] }`.
- `PATCH /v1/admin/publishers/{publisher}` (`admin-publisher-edit`) `{name?,
  email?}`: the same `422`s and `409`. A new name reaches the games at their
  next publish.
- `POST /v1/admin/publishers/{publisher}/setup` (`admin-publisher-setup`): a
  new one-time link, for a first sign-in or a forgotten password.
- `POST /v1/admin/publishers/{publisher}/disable` and `/enable`
  (`admin-publisher-disable`, `admin-publisher-enable`): switching one off
  ends every session it holds at once; its games stay as they are.

A publisher, as the console reads it: `{ id, name, email, status, games,
created_at, seen_at, setup_expires_at }`; `status` is `invited` (no password
yet), `active` or `disabled`.

Audit (area `admin`, subject the publisher): `publisher.created`,
`publisher.edited`, `publisher.setup_issued`, `publisher.disabled`,
`publisher.enabled`; `publisher.first_seen` (actor the publisher) on a first
sign-in.

### Review

- `GET /v1/admin/review` (`admin-review`) `?status=in_review|approved|rejected|all`
  (default `in_review`; `status: unknown`): `{ items: [{build summary, game:
  {id, name, icon, listing}, publisher: {id, name}}], waiting }`, the oldest
  submission first for `in_review`, else the newest decision first, at most
  100. `waiting` is how many are in review, whatever was asked.
- `GET /v1/admin/builds/{build}` (`admin-build`): the build in full, its game,
  its publisher, the game's live invite codes.
- `POST /v1/admin/builds/{build}/approve` (`admin-build-approve`) `{note?}`
  and `POST /v1/admin/builds/{build}/reject` (`admin-build-reject`) `{note}`:
  `409 not_in_review`; `422` `note: required` (a rejection), `note: too_long`
  (2000). Audit `build.approved` / `build.rejected` (area `games`, subject the
  game, `{build, version}`).
- `POST /v1/admin/builds/{build}/invite` (`admin-build-invite`): a code for
  the reviewer to test with, labelled "Review", one use, 7 days (it counts
  towards the game's 20).
- `GET /v1/admin/games/{game}/builds` (`admin-game-builds`): every build of a
  game, newest first, for its page.

`GET /v1/admin/games` and `admin-game` carry `owner: {id, name} | null` (the
publisher account; the old `publisher` field is still the manifest's words)
and `build: {id, status, version} | null` (the open build).
