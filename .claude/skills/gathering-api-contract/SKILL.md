---
name: gathering-api-contract
description: >-
  Add or change an operation of the Gathering platform's API (this repo,
  Go, huma v2 on chi) end to end: the contract in docs/ first, the rule in
  internal/biz with its *errors.Error codes, a numbered forward-only
  migration, the model's fields, the huma handler in the caller's group with
  its scope, role or permission metadata, guarded_test.go, huma's rule that a
  schema is named by its bare Go type name, the e2e sweep's steps, the
  developer docs page, the console page when the caller is an admin, and the
  status line in CLAUDE.md. Use it whenever someone adds or changes a /v1/...
  route, an /oauth or /.well-known document, a refusal code, a table or a
  column, a frame on the relay or a field in an answer; asks why Mount
  panics, why a route came out public, or why the console shows a raw code;
  or says "add an endpoint", "new operation", "change the contract", "add a
  migration", "expose this to the console".
---

# An API operation, end to end (gathering)

One binary, three kinds of caller, one error body. An operation is done when
its contract says what it answers and what it refuses, the rule enforces
exactly that, the handler only translates, the tests prove every refusal,
the sweep drives it against a running stack, and whoever reads it (a studio,
the app team, an admin) has it in words. Work in the order below: each step
reads the one before it.

## Read these first

- `CLAUDE.md`: the commands, the architecture, the rules and the status
  lines, which say what each milestone fixed in its contract first.
- The contract that owns the area: `docs/ROOMS.md` (rooms, the relay,
  results, rounds), `docs/FRIENDS.md` (friends, the inbox, a game's
  notifications), `docs/JOINING.md` (invite links), `docs/CONSOLE.md` (the
  console's routes). A new area gets a file of its own in the same shape.
- One complete operation to mirror, admin-only and with every layer:
  `admin-room-close`. Its contract is `docs/CONSOLE.md` "Rooms", its rule
  `Close` in `internal/biz/rooms/console.go` with its tests in
  `console_test.go`, its handler `internal/http/admin/rooms_handler.go`, its
  guard `TestConsoleChangesNeedTheAdminRole` in `internal/http/guarded_test.go`,
  its sweep `TestConsoleRooms` in `cmd/e2e/console_test.go`, its page
  `web/console/src/pages/room.jsx`.

## The layers at a glance

```
docs/<AREA>.md                          the contract: shapes, every refusal, their order
internal/data/migrations/000NN_*.sql    forward-only, numbered, applied by the api at boot
internal/data/model/*.go                the rows; import nothing of ours but enum
internal/data/enum/*.go                 the closed lists a column may hold
internal/biz/<area>/*.go                the rule, its *errors.Error codes, the wire's views
internal/http/{player,admin,guest}/     huma.Register with metadata; no rules
internal/http/router.go, cmd/api/main.go    Deps, Mount, the wiring
internal/http/guarded_test.go           public on purpose, or guarded
cmd/e2e/*_test.go                       the sweep against a running api (tag e2e)
web/docs/src/content/*.mdx              what a studio reads (pages.json)
web/console/src/pages/*.jsx             what an admin uses
CLAUDE.md                               the status line
```

## 1. The contract

- Write the operation into its doc before any code: the operation id, the
  method and path, the body, the answer (status and shape), every refusal
  with its status and code, and the order they are checked in wherever two
  can apply at once. Field problems are one `422 invalid_fields` with
  `fields: {field: problem}`, every problem at once.
- Names: operation ids lower case with dashes (`room-finish`,
  `admin-notify`); codes snake case (`room_closed`, `not_member`); a field's
  problem as `field: problem` (`title: too_long`, `scores.0.rank:
  must_be_positive`, `audience.players: nobody`).
- Times are UTC to the second (`2026-10-21T10:00:00Z`). Ids carry their
  prefix; a new kind of id gets one in `internal/pkg/ids`.
- A list is paged by an opaque cursor: `{items, next_cursor}`, `limit`
  checked by the rule (`limit: 1_to_50`, `limit: 1_to_100`), and anything the
  route never handed out is `cursor: invalid`.
- When the doc is ambiguous or wrong, fix the doc first, and list each fix
  in the milestone's status line ("fixed first: …").

## 2. The rule

- One package per area under `internal/biz/`. Work that spans two areas
  lives at the root (`biz.Dashboard`).
- Refuse with `*errors.Error` from `internal/pkg/errors`: `NotFound`,
  `Forbidden`, `Conflict`, `Unauthorized`, `Invalid(map[string]string)`,
  `TooManyRequests`, `ScopeRequired`, `InAnotherRoom`, and `Internal(err)`
  for everything nobody planned. Keep a package's refusals as variables
  named as the doc names them (`errRoomClosed`). A handler returns them as
  they are; a code is never translated on its way out.
- Gather field problems into one map and refuse once.
- A change runs in `data.NewTx(db).Do(ctx, fn)`, and every query goes
  through `data.Conn(ctx, db)` so it joins that transaction. Hold the row
  being changed (`SELECT … FOR UPDATE`: the rooms' `lockRoom`) and write the
  change's own history (`logs.room_events`) inside the same transaction.
  The rooms turn a transaction's error back into the API's with `asError`.
- After the commit, never inside it: frames to the relay (`Publisher`), and
  the audit line (`audit.Log`, opened per area in `cmd/api/main.go`; it
  writes on the log store's own pool, and a failed write is logged, never
  returned).
- The views the wire carries live with the rule (`rooms.Room`,
  `inbox.Feed`, `rooms.ConsoleRoom`), with `json`, `doc`, `example` and
  `enum` tags; huma documents them as they stand.
- Tests run on a real Postgres: `biztest.DB(t)` gives each package a
  database of its own and skips without `TEST_DATABASE_URL`; seed with
  `SeedPlayer`, `SeedGame`, `SeedAdmin`, `SeedLibrary`; fake only what is not
  ours (`biztest.Audit`, `biztest.Files`, the rooms' `fakePublisher`). Prove
  every refusal, their order where it matters, and that a refusal wrote and
  published nothing.

## 3. The migration

- `internal/data/migrations/000NN_<what>.sql`, the next number, with goose's
  `-- +goose Up` and the `Down` the others carry. A migration that has
  shipped is never edited: add the next one. The api applies them on start,
  under an advisory lock.
- Types as the schema has them: `varchar(24)` for our ids, `varchar(64)` for
  a game's, `timestamptz`, `jsonb` for a document. A comment on every column
  that is not obvious, and an index for every read the contract makes.
- The tests empty every table between runs by reading the catalogue, so a
  new table needs nothing more there.

## 4. The model

- The row in `internal/data/model/<area>.go`, with its `TableName()`: a
  nullable column is a pointer, a document is `Raw`, `NullRaw`, `Strings` or
  `Scores`. A model imports nothing of ours but `enum`; `.golangci.yml`
  (depguard) and `internal/pkg/arch` hold it to that.
- A closed list is an `enum` type with its list and `Valid`. A new value
  (`EndAdmin` joined the end reasons) goes in the list, in the view's
  `enum:"…"` tag, and in every place the docs list the values.
- A new audit action is a constant in `model/audit.go`.

## 5. The handler

- In the caller's group: `internal/http/player` (a player's session),
  `internal/http/admin` (the console's session, in its cookie),
  `internal/http/guest` (a game's server, or nobody yet). Register it in the
  group's `Register`, and hand the handler what it needs through `Deps` in
  `router.go` and the wiring in `cmd/api/main.go`.
- `huma.Register(api, huma.Operation{OperationID, Method, Path, Summary,
  Description, Tags, Metadata, DefaultStatus}, h.method)`. The description
  names the refusals.
- Metadata, from `internal/http/authn`: `authn.ScopeKey` is the kind of
  token (an operation without it is public); `authn.RoleKey` narrows to a
  role: the console reads with scope `admin` alone, and every change adds
  `authn.RoleKey: string(enum.AdminRoleAdmin)`; `authn.PermKey` narrows a
  game's token to one permission its manifest asked for (`results:write`,
  `notify:send`). Plain strings only.
- The handler translates and nothing more: path, query and body in, the
  rule called with `caller.FromContext(ctx).Subject`, its answer or its
  error out. A body field the rule checks carries `omitempty`, or huma makes
  it required and refuses in its own words before the rule can; say the
  codes in its `doc:` tag. A status chosen at run time is a `Status int`
  field on the response (`201` for a send, `200` for a dry run).
- huma names every schema by its bare Go type name, across the whole API.
  Two different types called `Room`, or two request types both called
  `notifyRequest` (each anonymous `Body` becomes `NotifyRequestBody`), panic
  at Mount. Name types for where they live: `friends.PlayerCard`,
  `rooms.ConsoleRoom`, `announceRequest`.
- `nullable:"true"` belongs on a pointer to a scalar; on a pointer to a
  struct Mount panics. A struct pointer is null by being nil.
- `go run ./cmd/api openapi` prints the document with no stack running: the
  quickest proof that Mount takes the new types.

## 6. guarded_test.go

- `TestEveryRouteIsGuardedOrNamedPublic` fails on an operation with no scope
  unless `publicOperations` names it with a reason a reviewer can read;
  `TestNothingIsExcusedThatDoesNotExist` fails on a name there that no route
  registers; `TestEveryRuleIsAPlainString` on metadata that is not a plain
  string; `TestConsoleChangesNeedTheAdminRole` on a console change (POST,
  PUT, PATCH or DELETE under scope `admin`) without the admin role, unless
  `consoleOpenToEveryRole` names it.
- Making a route public is a decision: add it to `publicOperations` with
  why, in the same change.

## 7. The sweep

- `cmd/e2e`, built with the `e2e` tag; `make e2e` with `E2E_API`,
  `E2E_CODE`, `E2E_CONSOLE_ORIGIN` and, for the fixtures, `E2E_FIXTURES`.
  A test registers a game of its own (`serveStudio`), signs its own players
  up (`player`), opens the console with the dev sign-in, and suspends its
  game when it is done.
- Drive the operation as its caller does: `client.call(method, path, body,
  want, &out)`; sockets with `connect`, `expect` and `closed`; a frame that
  must arrive in time through `heard`.
- The stack is shared with whoever else is using it: never send to everyone
  (a dry run is fine) and never change rows the sweep did not make.
- Each proven step logs one line, `t.Logf("ok   …")`.

## 8. The words for whoever reads it

- A studio's operation (the guest group, the bridge, a frame, a ticket):
  its page in `web/docs/src/content/`, listed in `web/docs/pages.json`, in
  the docs' voice and with every refusal; then `npm run build && npm run
  lint` in `web/docs`.
- An admin's operation: the console in `web/console/src`. Calls go through
  `api.js`; every refusal reaches the screen in words through `errors.js`
  (`sentences` for a code, `fieldSentences` for `field:problem`); a page in
  `pages/`, its route in `app.jsx`, its place in the rail in
  `components/shell.jsx`. A change waits behind a confirmation that says
  what will happen, and is disabled without the admin role
  (`useMe().roles`). It works at a laptop's width and at a phone's. Then
  `npm run lint && npm run build` in `web/console`.
- The app's side is the app team's: the contract says what the app must do,
  and the platform side does not edit the app.

## 9. Done

- `make check` with `TEST_DATABASE_URL` and `TEST_REDIS_URL` set; `make
  lint`, and `golangci-lint run --build-tags e2e ./...` in `backend/`, with
  0 issues; `make e2e` against an api restarted on the final code;
  `scripts/brand-check.sh` clean, `.claude/` included.
- `CLAUDE.md` gets a status line in the shape of the others: "- M<n> (<what>,
  platform side) done <date>, to `docs/<X>.md` (fixed first: …): migration
  …, `biz/…` (…), the routes (their operation ids), the docs' and the
  console's pages, the e2e steps."

## Common mistakes

- A code translated in a handler, or a refusal the contract never names.
- A rule in a handler: a check or a query belongs in `biz`.
- A second type with a schema name already taken; `nullable` on a struct
  pointer.
- An operation with no metadata: it is public.
- A console change without the admin role.
- A frame or an audit line written inside the transaction: a rollback
  cannot take back what a socket already heard.
- A shipped migration edited instead of a new one added.
- A sweep step that sends to everybody, or changes what it did not make.
- A name, a font or a colour borrowed from the product this one was
  modelled on: `scripts/brand-check.sh` fails on it, here too.
