# CLAUDE.md

Guidance for Claude Code in this repository.

## Project overview

**Gathering** (جمعة) is the platform behind the minigame super app: one
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
make e2e            # the in-repo sweep against a running api (E2E_API, E2E_CODE, E2E_CONSOLE_ORIGIN)
make keys           # a new signing key
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
