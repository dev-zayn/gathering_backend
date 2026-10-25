# CLAUDE.md

Guidance for Claude Code in this repository.

## Project overview

**Gathering** (گعدة) is the platform behind the minigame super app: one
phone sign-in, a player profile, friends and presence, the game registry and
library, rooms with a realtime relay, results and the inbox. Go, in
`backend/`; a React admin console in `web/console`; developer docs in
`web/docs`. Modelled on `../super-gini` (read-only reference) but a separate
product with its own brand: `scripts/brand-check.sh` fails on any identifier
borrowed from the reference's.

The build plan is `../GATHERING_PLAN.md` (routes, migrations, the relay, the
milestones); the brief is `../GATHERING_PROMPT.md`.

## Commands

```bash
make env            # backend/.env from the example + a fresh JWT_PRIVATE_KEY
make up / down      # the laptop stack (docker compose)
make run            # go run ./cmd/api
make check          # brand-check, gofmt, tidy-check, ui-check, vet, go test -race
make lint           # golangci-lint (CI runs v2.13.2)
make e2e            # the in-repo sweep against a running api (E2E_API, E2E_CODE, E2E_CONSOLE_ORIGIN;
                    # E2E_FIXTURES too, with the fixtures started on fixture.xo's PARTNER_CLIENT_SECRET);
                    # joined/left must reach a socket within 1 s, each logged as a "time" line
(cd backend && golangci-lint run --build-tags e2e ./...)   # lint the sweep's files too
make keys           # a new signing key
make games          # build the arcade's five games in ../gathering_games (each its own bun project, into its dist/)
make fixtures       # the fixtures on :8091 over ../gathering_games (xo, tap as they are; a built game from its dist/)
scripts/games-on-wifi.sh [ip]   # after the Mac changes network: the fixtures on :8093 at the new address, every game moved there
go run ./cmd/roomplayer -code K7M2QX -taps 10 -rounds 2   # (in backend/) a headless member: signs
                    # up with OTP_FIXED_CODE, joins by code, taps the tap race in every round over the
                    # relay, prints its place per round, leaves after -rounds finished rounds
```

Go tests need `TEST_DATABASE_URL` (and `TEST_REDIS_URL` for the whitelist
and the relay) or every database test skips; on this Mac Postgres 16 and
Redis run from Homebrew: `TEST_DATABASE_URL='postgres://zayn@localhost:5432/postgres?sslmode=disable'
TEST_REDIS_URL='redis://localhost:6379/1'`. Docker is not required for
`make check`. To run the api by hand: `DEBUG=true DEV_ADMIN=true
FILES_DIR=.files OTP_FIXED_CODE=123456 INSECURE_COOKIE=true` plus the stores
and `JWT_PRIVATE_KEY` (`make keys`); the sweep then signs in with that code.

## Architecture (backend/internal)

- `http/` is the HTTP surface and holds no rules: `server.go` (huma v2 on
  chi, CORS for named origins, one error body `{error, fields?}`),
  `router.go` (`Deps`, `Mount`), `authn/` (per-operation scope metadata:
  `authn.ScopeKey`, `RoleKey`, `PermKey`; an operation without a scope is
  public and must be named in `guarded_test.go`). Route groups by caller:
  `player/`, `admin/`, `guest/`, `ws/` (arriving with their milestones).
- `biz/` holds the rules, one package per area; `data/` is Postgres (GORM over
  pgx), Redis, the embedded goose migrations applied at boot under an
  advisory lock, and `model/` (the rows: imports nothing of ours but `enum`).
- `pkg/` is what any layer may use and no layer owns: `token` (ES256 JWT, the
  JWKS), `caller` (who is calling, from the token's claims), `errors` (the one
  error type and its codes), `ids` (`ply_`, `gam_`, `room_`…), `phone`
  (E.164, Iraq), `log`. `.golangci.yml` and `pkg/arch` enforce the layering.
- `conf/conf.go` reads every environment variable; `backend/.env.example`
  lists them. `DEBUG=true` is a laptop: codes to the log, `DEV_ADMIN` opens
  the console without a provider, `FILES_DIR` stands in for S3.
- Tokens: `otp`, `signup`, `session`, `admin` (ours, minted after an OIDC
  provider says who the person is), `game` (a game's backend), `room` (a
  ticket a game's server verifies via JWKS).

## Rules

- Biz returns `*errors.Error`; handlers return it as is. Never translate codes
  in a handler.
- Every migration is forward-only and numbered; the api migrates itself.
- Secrets only in `.env`; never in code, docs or fixtures.
- Conventional commits with a scope; branches `feature/…`; PRs target
  `development`.

## Status

- M0 (scaffold) done 2026-10-20.
- M1 (identity) done 2026-10-20: migrations 00001–00004 (players, sessions,
  admins, logs), phone sign-in with the OTP webhook or the log sender,
  sessions and refresh rotation, the player record (`/v1/me`, presence,
  search, cards), devices, the console session (any OIDC provider through
  `pkg/sso`, or the dev sign-in), admins, players and the audit log in the
  console, the e2e sweep (`cmd/e2e`).
- M3 (games, platform side) done 2026-10-20: migrations 00005–00006 (games,
  scopes, library; oauth_codes, game_tokens), the manifest with its `room`
  block (`model.Manifest.Check`), `biz/games` (catalogue, library, registry,
  held manifests, pictures through `pkg/files`: S3 or `FILES_DIR` served at
  `/files/*`), `biz/guests` (the launch mint, `/oauth/token` and
  `/oauth/authorize` with PKCE, the bridge context), the player, guest and
  admin routes, the console's Games pages, the docs' Authentication and
  Getting listed, the fixtures (`xo`, `tap`, the partner backend under
  `/partner/`), the e2e games sweep. Rooms are M4; `Notify`/`Results` on the
  bridge arrive with their milestones. 2026-10-21: a store row carries the
  game's `permissions` (the app asks for consent before a tile opens a game),
  and the console's cookie is `gathering_admin` (`__Host-` when Secure);
  `scripts/brand-check.sh` also fails on the reference's `sg_` prefix.
- M4 (rooms and the relay, platform side) done 2026-10-21, to
  `docs/ROOMS.md`: migration 00007 (rooms, room_members; `logs.room_events`
  is 00004's), `biz/rooms` (the rules, the views and every frame, the ticket,
  the sweeper job `rooms`), `biz/relay` (one hub per process, one Redis
  subscription per room, the Lua publish and state scripts, `relay.Socket` so
  the rules run without a network), `http/player` rooms routes, `http/ws`
  (the socket, `github.com/coder/websocket`, the only importer: depguard),
  the room check on the launch and the room on the bridge context, the
  docs' Rooms page, the e2e rooms sweep; the fixtures serve the app's own
  games (`make fixtures-sync`).
- M5 (results, platform side) done 2026-10-21, to `docs/ROOMS.md` "Results
  (M5)": migration 00008 (`results`, kept for good: no foreign key on
  `room_id`, the room's code and each player's seat copied in), the host's
  end writing the rows in its transaction unless the game's server wrote
  first (then every end carries the game's results), `POST
  /v1/bridge/results` (`bridge-results`, scope `game` + `results:write`:
  replaces the room's rows whole, rewrites an ended room's `rooms.results`,
  a `results` room event), `GET /v1/me/history` (cursor paging) and `GET
  /v1/rooms/{room}/results` (answers after the room row is gone), the Room's
  `results` read from the table (a room ended before M5 falls back to
  `rooms.results`), the fixtures' `POST /partner/verify-ticket` (ES256
  against the JWKS, cached five minutes), the docs' Rooms page (posting
  results, the refusals, verifying a ticket), the e2e results steps. The
  sweep's `/partner/exchange` step needs `PARTNER_CLIENT_SECRET` on the
  fixtures (fixture.xo's secret).
- M6 (friends and the inbox, platform side) done 2026-10-21, to
  `docs/FRIENDS.md` (fixed first where it was ambiguous: refusal orders, the
  friend notifications' words and empty links, a stale room in presence,
  switched-off players, the leaderboard's `game` and ties, the bridge's
  field codes and what its limit counts): migrations 00009 (`friendships`,
  one row per pair lower id first; `blocks`) and 00010 (`notifications`,
  the key upsert on `(player_id, COALESCE(game_id, ''), key)` keeping the
  id), `biz/friends` (request with the mutual auto-accept, accept, decline,
  cancel, remove, block and unblock, each pair change under a pair advisory
  lock with its notification in the same transaction; the card by handle or
  id with `friendship`, `mutual_friends` and presence between friends only;
  friends by presence; the friends' leaderboard from `results`; `Among`,
  which the rooms ask), `biz/inbox` (raise, the feed with cursor paging and
  the unread count, read by ids or all, the 90-day sweep in the clock's
  `sweep` job), rooms' `visibility: friends` (`friends_only`, right after
  `kicked`) and `POST /v1/rooms/{room}/invites` (`room-invite`, key
  `room_invite:{room}`), `POST /v1/bridge/notifications` (`bridge-notify`,
  scope `game` + `notify:send`, 10 a player per game per hour through
  `identity.Limiter`), `players.Card` and `games.Tile` (huma names schemas
  by bare type name: a second `Card` or `Room` panics at Mount, hence
  `friends.PlayerCard`), the docs' Notifications page and the bridge's
  `friends.*` and `room.invite()`, the e2e friends sweep (`TestFriends`).
- M6.1 (rounds, platform side) done 2026-10-21, to `docs/ROOMS.md`
  "Rounds" (fixed first: the migration's round for rooms with rows, the
  copy in `rooms.results` and what an end between rounds carries, the
  sweeper's finish, the bridge's refusal order and `round` in its answer,
  per-round access and `rounds` on the sheet, the cursor): a room plays
  again in the same room. Migration 00011 (`rooms.round`, `results.round`
  in the key and in `results_history`), `Start` counting the round with the
  relay's reset in the same Lua step as `started` (`Publisher.Start`,
  `relay.Hub.Start`), `Finish` (`room-finish`, `POST /v1/rooms/{room}/finish`:
  the round's results, `finished`, the room back in its lobby, no socket
  closed), `End` finishing a round under way and refusing scores in the
  lobby (`scores: not_playing`), the last leave and the sweeper finishing
  too (a round past `ROOM_MAX_PLAY` is finished `timeout`, then its lobby
  idles), results, history (cursor with the round), `room-results?round=`
  (`round`, `rounds`) and the bridge's results (`round`) per round, the
  ticket's `round` claim (the fixtures answer it), the bridge context's
  `round`, `cmd/roomplayer -rounds N`, the e2e rooms sweep playing two
  rounds in one room, the docs' Rooms and Bridge pages.
- M7 (invite links, platform side) done 2026-10-21, to `docs/JOINING.md`
  (fixed first: the not-found and try-again forms and the words on each,
  how `Accept-Language` decides, the exact headers and policy, the link
  host as a proxy, the documents' operation ids, the env names it
  replaces): `GET /r/{code}` (`invite-link`, an HTML page in
  `http/guest/links_*.go` over `rooms.Service.LinkCard`, which reads the
  code like a typed one and answers the game and the code alone: 200 while
  the room is open, between rounds included, 404 not found, 500 try again;
  Arabic, English by `Accept-Language`; `no-store`, a CSP allowing the
  copy key's script and the one style by hash and pictures from the icon's
  own host), `GET /.well-known/assetlinks.json` and
  `/.well-known/apple-app-site-association` (`assetlinks`,
  `apple-app-site-association`: empty until `APP_LINKS_SHA256` /
  `APPLE_TEAM_ID`, `max-age=3600`), `APP_ANDROID_PACKAGE`,
  `APP_IOS_BUNDLE`, `APP_STORE_URL`, `PLAY_STORE_URL` (M0's unread
  `APP_BUNDLE_ID` and `STORE_URL_*` gone), the docs' Rooms "Invite links",
  the e2e rooms sweep reading the page (fresh, between rounds, ended, a bad
  code) and both documents.
- M8 (the console's last pages, platform side) done 2026-10-21, to
  `docs/CONSOLE.md` (fixed first: a console change takes `authn.RoleKey`
  `admin`, not `authn.PermKey`; an audit line is written after the commit
  on the log's own pool, `room.closed` in area `rooms` and
  `notification.sent` in `players`, subjects and details named; the rooms
  list's `{items, next_cursor, live}` and its refusals; an event's `player`
  null where nobody was; the close's event detail carries `round`; a
  players audience reads an id-shaped ref as an id and anything else as a
  handle, `@` and case ignored, once per player; an empty kind or game is
  `required` / `not_found`; a game in any listing; an `all` or `game`
  audience reaching nobody is not refused; a dry run refuses as a send; the
  announcements' page and limits; `rounds_today` from the `started`
  events, `rooms_live` kept): `enum.EndAdmin` (`admin` in ROOMS.md, the
  Room's `end_reason`, the `ended` frame), migration 00012
  (`announcements`, `ann_` ids), `biz/rooms` console.go (`List`, `Detail`,
  `Close`: an end with nobody named, a round under way finished as by any
  end; the rooms audit area), `biz/inbox` announce.go (`Announce` with its
  dry run, one `system` row per recipient in one transaction with the
  announcement, `Announcements`), `biz.Dashboard` (`rooms`, `rounds_today`,
  and `Tally`: a player's friends, rounds and open room), the routes
  `admin-rooms`, `admin-room`, `admin-room-close`, `admin-notify`,
  `admin-announcements` and the new fields of `admin-dashboard` and
  `admin-player`, `TestConsoleChangesNeedTheAdminRole`, the console's
  Rooms, Room and Notifications pages and the new numbers on Dashboard and
  Players, the e2e steps (`TestConsoleRooms`, `TestConsoleAnnouncements`,
  and `joined`/`left` timed in `TestRooms`), the docs site reviewed against
  the code (navigation, the bridge's promises and refusals, `admin` among
  the end reasons), the README runbook, the `gathering-api-contract` skill
  (`.claude/skills/` now kept in git), CI pushing the fixtures image, and
  brand-check reading the reference's three-letter prefix in text files
  only (a picture's bytes spelled it).
- Rooms from inside a game (platform side) done 2026-10-22, to
  `docs/ROOMS.md` "Rooms from inside a game": `room-join` takes an optional
  `game` and refuses another game's code with 409 `another_game` right after
  the code is found, a member of that room too (the Joining list and the
  HTTP table say so); its rooms tests, an e2e rooms step, the docs' Rooms
  and Bridge pages (`room.create`, `room.join`, the Join sheet, the
  refusals, the frame menu's two entries).
- Push (platform side) done 2026-10-22, to `docs/PUSH.md` (fixed first: a
  game's own notification collapses on its id, not its key, which two games
  can share; an ended session's phone is skipped, and the sweep deletes it;
  the `would push` line's exact form; `E2E_FCM`): `pkg/fcm` (FCM HTTP v1,
  one call per token, OAuth 2 from the service account's JSON key through
  `golang.org/x/oauth2/jwt`, now direct at v0.37.0; refusals sorted into
  `Gone` and `Temporary`, the token cut out of every error) and
  `pkg/fcm/fcmtest` (a fake Firebase: the token exchange and
  `messages:send`, scripted answers, held sends), `biz/push` (one planner
  and 8 senders per process; a raise never waits, a full queue drops with a
  log line; the message per kind; a gone token deletes its device row by
  id; a 429 or 5xx tried once more; without a key one `would push` line per
  notification; the api's close drains for 5 s, then cancels),
  `data.AfterCommit` (run by `Tx.Do` after the commit, at once outside a
  transaction, never on a rollback) and `inbox.Inbox.Pusher` (every
  `Raise`, the key upsert included, and every row of an `Announce`),
  `identity.Devices.Live` (live sessions of players not switched off) and
  `Drop` by id, `FCM_PROJECT_ID`, `FCM_CREDENTIALS_FILE`, `FCM_ENDPOINT`
  (M0's base64 `FCM_CREDENTIALS` and unread `PUSH_PER_GAME_HOURLY` gone),
  `scripts/fcm-test-key.sh`, the e2e `TestPush`, the docs' Notifications
  page, the README's key runbook. Once the project's key was in
  (`backend/.env`: `FCM_PROJECT_ID`, `FCM_CREDENTIALS_FILE`, the key file
  itself outside the repo), the same day: the address is the phone's
  Firebase installation id, sent as `message.fid` (`fcm.Message.To` by
  `fcm.IsFID`; any other address still as `token`, and the route's field
  keeps its name), and every send Firebase takes is logged `push sent`
  with the notification and device ids. A friend request then reached an
  Android emulator's foreground and tray through the project (the app's
  `E2E_TEST=push`).
- Tournaments (platform side) done 2026-10-22, to `docs/TOURNAMENTS.md`
  (written first; ROOMS.md, FRIENDS.md, PUSH.md, JOINING.md and CONSOLE.md
  amended to it): migration 00013 (`tournaments` with `trn_` ids and codes
  from the rooms' alphabet, `tournament_entrants`, `tournament_matches`
  with `trm_` ids and void sides, `tournament_plays` — one counted round —
  `rooms.match_id` with one open room per match, `rooms.end_reason` widened
  to 12), `enum.EndTournament` (`tournament` in every end-reason tag),
  `enum.NoticeTournament`, the tournament enums, `biz/tournaments` (create
  and the console's official one, join by code, leave/withdraw, remove,
  invite; the draw in `begin` — crypto shuffle, the standard bracket order,
  byes settled; `settle`/`decide`/`finishLeague` on a `state` read whole
  under the tournament's row lock; `Count`, `Admits` and `Brief` for the
  rooms through `rooms.Matches`; `Play` through `rooms.Seat`; award,
  finish, cancel; the views, the lists with cursors, the link card; the
  notifications; the clock's `tournaments` job), the rooms' match door
  (`not_in_match`), `tournament_room` on kick and invite, every round that
  closes counted in the room's transaction and the deciding one ending the
  room `tournament` (`decidedEnd`; `CloseMatchRoom` after a decision made
  elsewhere, run by `data.AfterCommit` on `data.Outside(ctx)`, new), the
  Room's and the bridge context's `tournament` (`rooms.MatchBrief`), the
  routes (`tournament-*`, `tournaments`, `admin-tournament*`), `GET /t/{code}`
  (`tournament-link`, the invite page generalised) and `/t/*` in the
  apple-app-site-association, `tournament` and `match` in a push's data,
  the audit area `tournaments`, `TOURNAMENT_MAX_ENTRANTS` and
  `TOURNAMENT_OPEN_FOR`, the console's Tournaments pages, the docs'
  Tournaments page. Built and vetted, the OpenAPI document printed; the
  rules' tests and an e2e sweep for tournaments are still to be written.
- The games moved out (2026-10-23, the user's ask "move the minigames out
  all project each into its own directory"): every game is a folder of the
  workspace's `../gathering_games/`, in no repo, beside its own manifest
  (`.well-known/`). `xo` and `tap` came from the app (which no longer
  bundles them) as they were; the arcade (`games/arcade`, gone) split into
  five standalone bun projects, each with its own copy of the kit cut down
  to what it uses, built into its `dist/`. `deploy/fixtures/` is gone; the
  fixtures serve `../gathering_games` and answer a game that has a `dist/`
  from it (`games.built`, `cmd/fixtures/games_test.go`); compose mounts it;
  `make games` builds the five and runs `scripts/brand-check.sh
  ../gathering_games` (the script now takes folders); `make fixtures` runs
  the server over it.
- Hetzner (2026-10-24, the user's domain ga3da.fun; fixed sign-in code and
  no console provider for now, by the user's choice): `deploy/hetzner/`
  (`compose.yml` built on the server with Postgres, Redis and Caddy;
  `Caddyfile`: the api's host refuses `/v1/admin/*`, the console's host
  proxies `/v1/*`, the bare domain carries the links;
  `setup.sh` makes the secrets in /opt/ga3da/secrets; `deploy.sh` from the
  Mac; `games.sh` registers, gives icons and publishes). Caddy's routing
  checked locally; not yet run on a server. CI's govulncheck action pinned
  to v1.1.0 (v1.8.0 never existed, so the check job never ran).
- The console's password sign-in (2026-10-25, the user's ask: one sign-in,
  not Caddy's dialog and the console's screen): `admin-session-password`
  (`identity.Console.OpenPassword`, bcrypt against `CONSOLE_PASSWORD_HASH`,
  20 tries in 15 minutes for everybody, subject `password:<name>`), the
  console's card showing only the ways in its build has
  (`VITE_PASSWORD_SIGNIN`), Caddy's basic auth and the server's
  `DEV_ADMIN` gone (`setup.sh` moves an existing server's password over).
  The players list is the console's own `PlayerStore.List` (everybody,
  newest first, the switched off included); it was the players' search,
  which answers nobody for an empty box (docs/CONSOLE.md). The e2e
  `TestConsolePasswordSignIn` (with `E2E_CONSOLE_PASSWORD`, else skipped)
  and the newest-first step in `TestConsole`.
- The console's design (2026-10-25, the user's ask): the گعدة brand
  (../gathering_brand) in `web/console/src/index.css`: its palette as the
  tokens (Amber the main action with Navy ink, `--color-accent-text` for
  amber words, Indigo second), a dark page (Navy, Slate, Gray) following the
  system or the rail's switch (`lib/theme.js`, `data-theme` set by
  `public/boot.js`), IBM Plex Sans Arabic, Cairo for headings and Geist Mono
  for codes and numbers (`src/fonts`, cut to Latin and Arabic, OFL), `chip`
  for filter keys (`aria-pressed`), sentence-case `label`s; the rail on Navy
  with the brand's wordmark (`public/brand/`, never set in a font) and a
  drawer on a phone; the sign-in beside the brand's pattern and lockup; the
  dashboard's groups as cards; a game's tile falling back to its letter.
