# Gathering (جمعة)

The minigame super app's platform: one sign-in, one player profile, friends,
rooms, and the games as HTML mini apps inside the app. This file is for
whoever runs it. What the code does is in the code; the contracts are in
`docs/` (ROOMS, FRIENDS, JOINING, CONSOLE); what a game developer needs is in
`web/docs`.

## What is in the repository

    backend/    the Go module: the api, the fixtures, cmd/roomplayer (a headless
                member of a room) and cmd/e2e (the sweep, built with the e2e tag)
    web/console the admin console, React, static files behind nginx
    web/docs    the developer docs, static files behind nginx
    deploy/     the Dockerfile for the Go binaries, compose for a laptop and a server,
                the Postgres init, the fixture games
    docs/       the contracts the platform and the app build to
    scripts/    brand-check (no borrowed brand identifier may land here), fcm-test-key
                (a throwaway key for the sweep's fake Firebase)

Three things run: the **api** (8080), which also runs its own clock to tidy
its tables, finish rounds that run too long and end rooms nobody is in, and
two static sites, the **console** (8085) and the **docs** (8092). They need
Postgres, Redis and somewhere to put pictures (S3, or a directory on a
laptop). Admins sign in through any OIDC provider; on a laptop `DEBUG=true` +
`DEV_ADMIN=true` opens the console without one. The **fixtures** (8091) serve
two real games and a tiny partner backend, for a laptop only.

## Running it on a laptop, in Docker

You need Docker, `make` and `openssl`.

    make env        # writes backend/.env from the example with a fresh signing key
    make up         # postgres, redis, api, console, docs, fixtures
    make logs       # the api; make logs S=console for another

- Console at http://localhost:8085 (dev sign-in), docs at http://localhost:8092
  (user/password `gathering`), API at http://localhost:8080 (`/docs` for the
  OpenAPI UI).
- `DEBUG=true` means no code is sent to a phone; it is in the api log:

      docker compose -f deploy/compose.yml logs api | grep "OTP NOT SENT" | tail -1

## Running it on a laptop, by hand

Go, Node 22, and Postgres and Redis running (Homebrew is fine). The api reads
`backend/.env` (`make env`), so point it at your stores and add the laptop's
switches: a database it may own (`createdb gathering_dev`), and

    DATABASE_URL=postgres://you@localhost:5432/gathering_dev?sslmode=disable
    REDIS_URL=redis://localhost:6379/0
    DEBUG=true  DEV_ADMIN=true  FILES_DIR=.files  ALLOW_PRIVATE_HOSTS=true
    OTP_FIXED_CODE=123456       # every sign-in takes this code (DEBUG only)
    INSECURE_COOKIE=true        # Safari keeps no Secure cookie from http://localhost

Then each in its own terminal:

    make run                                   # the api on :8080; it migrates itself
    cd web/console && VITE_API_URL=http://localhost:8080 VITE_DEV_ADMIN=true npx vite --port 8085
    cd web/docs && npm run dev -- --port 8092  # npm run dev:app for the app team's pages

The console calls the api across origins with its cookie, so `CORS_ORIGINS`
must name its origin (the example's `http://localhost:8085,http://localhost:8092`
does).

### The fixture games

The games live outside this repo, one folder each in the workspace's
`../gathering_games/`, each beside its own manifest (`.well-known/`): `xo` and
`tap`, plain pages served as they are, and the arcade's five party games,
projects of their own served from the `dist/` they build:

    make games      # bun install + build in each arcade game, then the brand check over them

Serve them, with the partner backend under `/partner/` (`FIXTURES_DIR` is
`../gathering_games`, `PORT` 8091):

    PUBLIC_URL=http://localhost:8091 API_URL=http://localhost:8080 \
      PARTNER_CLIENT_SECRET=… make fixtures

Register each from the console (Games, Register by URL:
`http://localhost:8091/xo/`, then `/tap/`), write its store copy and publish
it. The console shows `fixture.xo`'s client secret once: that is the
`PARTNER_CLIENT_SECRET` the fixtures need (`PARTNER_CLIENT_ID` defaults to
`fixture.xo`). With it, `POST /partner/exchange {code}` spends a launch code
at the api and reads the bridge, and `POST /partner/verify-ticket {token}`
checks a room ticket against the JWKS the way a studio's server would. In
Docker the fixtures are `http://fixtures:8091/…` to the api; restart them with
`PARTNER_CLIENT_SECRET` set in the shell that runs `make up`.

### A third player: `cmd/roomplayer`

A headless member for a device test on a machine that cannot run another
emulator. It signs a new player up with the api's `OTP_FIXED_CODE`, joins by
code, taps the tap race in every round over the relay, prints its place per
round and leaves after `-rounds` finished rounds:

    cd backend && go run ./cmd/roomplayer -code K7M2QX -taps 10 -rounds 2

`-api` (default `http://localhost:8080`), `-otp` (`123456`), `-nickname` and
`-timeout` (10m) are the others. In any game but the tap race it only sits at
the table.

### Push notifications

Every notification the inbox gains is also pushed to the player's phones
through Firebase Cloud Messaging, project `gathering-supergame`
(`docs/PUSH.md`). Without a key the api sends nothing and writes each push to
its log instead, which is all a laptop needs:

    {"time":"…","level":"info","prefix":"gathering","msg":"would push","notification":"ntf_…","devices":1}

To push for real from the laptop's api:

1. In the Firebase console, open `gathering-supergame`, then Project settings
   → Service accounts → Generate new private key. The file it downloads is
   the key.
2. Keep it outside the repository, for instance
   `~/.config/gathering/fcm-gathering-supergame.json` (`chmod 600`). It is a
   password for the project: never commit it, and never paste its contents
   into `backend/.env` or anywhere else.
3. Point the api at it in `backend/.env` and restart the api:

       FCM_PROJECT_ID=gathering-supergame
       FCM_CREDENTIALS_FILE=/Users/you/.config/gathering/fcm-gathering-supergame.json

The api reads the key once at boot and refuses to start on a bad one; the
log then says `pushing through Firebase`, and `push sent` (with the
notification and device ids) for every send Firebase takes. In Docker
(`make up`) the path is
the container's, so mount the key's directory into the api service and name
the path inside it. iOS also needs the APNs key uploaded in the Firebase
console (Project settings → Cloud Messaging).

## Checks

    make check   # what CI runs: brand-check, gofmt, tidy, vet, go test -race, the console and docs builds
    make lint    # golangci-lint v2.13.2, as CI runs it
    cd backend && golangci-lint run --build-tags e2e ./...   # the same, the sweep's files included

The Go tests need `TEST_DATABASE_URL` (and `TEST_REDIS_URL` for the
whitelist and the relay), or every database test skips and reports ok:
`TEST_DATABASE_URL='postgres://you@localhost:5432/postgres?sslmode=disable'
TEST_REDIS_URL='redis://localhost:6379/1'`. Each package makes a database of
its own next to it.

### The e2e sweep

`make e2e` runs `cmd/e2e` against a running api, as the app and the console
use it:

    E2E_API=http://localhost:8080 E2E_CODE=123456 E2E_CONSOLE_ORIGIN=http://localhost:8085 \
      E2E_FIXTURES=http://localhost:8091 make e2e

The api must run with `DEBUG`, `DEV_ADMIN`, `OTP_FIXED_CODE` (the sweep's
`E2E_CODE`), `ALLOW_PRIVATE_HOSTS` (the sweep serves games of its own on
loopback; `E2E_SELF=host.docker.internal` for an api in a container) and
`E2E_CONSOLE_ORIGIN` among its `CORS_ORIGINS`. It covers: sign-in and
sign-up, the records, rotation and sign-out; the console's numbers, players
and log; a game registered by URL, published, opened (the first open puts it
on the shelf), its code spent, tokens renewed and revoked; rooms joined by
code with both sockets on the relay (`joined`, `msg`, `state`, `ended`;
`joined` and `left` must reach the other socket within a second of the call,
and the sweep logs each time as a `time` line), two rounds in one room, the
ticket, results and history; the invite link's page and the two
`.well-known` documents; friends and the inbox; and the console's rooms (a
close heard as `ended` reason `admin`) and announcements (landing in chosen
players' inboxes). `E2E_FIXTURES` adds the fixtures: `fixture.xo` registered
and its partner backend spending a code and verifying a ticket, which needs
the fixtures running with `fixture.xo`'s `PARTNER_CLIENT_SECRET`.

The push step registers a phone and checks that a request reaches its
owner's inbox; an api without a key logs `would push` for it. `E2E_FCM`
makes the step check the send itself: the sweep serves a fake Firebase on
that address, so the api must run against it, with a throwaway key whose
`token_uri` is the fake's:

    scripts/fcm-test-key.sh http://127.0.0.1:8094/token > "$TMPDIR/gathering-fcm-test.json"
    # the api: FCM_PROJECT_ID=gathering-e2e FCM_ENDPOINT=http://127.0.0.1:8094 \
    #          FCM_CREDENTIALS_FILE=$TMPDIR/gathering-fcm-test.json
    E2E_FCM=127.0.0.1:8094 E2E_API=… make e2e

## Settings since M3

Every variable is in `backend/.env.example` with its default in
`internal/conf/conf.go`. New since the games milestone:

| Variable | Default | What it does |
|---|---|---|
| `RELAY_MAX_FRAME` | `16384` | the largest frame a socket may send, bytes |
| `RELAY_RATE` | `30` | frames a second per socket, pings included |
| `RELAY_MAX_MEMBERS` | `64` | the most members a room may hold, whatever the manifest says |
| `PRESENCE_TTL` | `60s` | how long a player's presence lasts without a ping |
| `ROOM_LOBBY_IDLE` | `10m` | a lobby nobody is connected to ends after this |
| `ROOM_MAX_PLAY` | `2h` | a round running longer is finished (reason `timeout`) |
| `ROOM_CODE_ALPHABET` | 32 symbols, no `O 0 I 1` | what a room code is drawn from (16 at least) |
| `TOKEN_ROOM_TTL` | `10m` | a room ticket's life |
| `LINK_PUBLIC_URL` | `http://localhost:8080` | where invite links live: `LINK_PUBLIC_URL/r/{code}` |
| `APP_ANDROID_PACKAGE`, `APP_LINKS_SHA256` | the app's package, empty | Android's claim on the link host; no fingerprint is no claim |
| `APPLE_TEAM_ID`, `APP_IOS_BUNDLE` | empty, the app's bundle | iOS's claim; no team is no claim |
| `APP_STORE_URL`, `PLAY_STORE_URL` | empty | where the link's page sends a phone without the app |
| `INSECURE_COOKIE` | `false` | drops Secure from the console's cookie; a laptop on Safari only |
| `FCM_PROJECT_ID` | empty (`gathering-supergame` in the example) | the Firebase project pushes go to |
| `FCM_CREDENTIALS_FILE` | empty | the path to its service account's JSON key, outside the repository; empty logs `would push` instead of sending |
| `FCM_ENDPOINT` | `https://fcm.googleapis.com` | Firebase's base URL; a test's fake otherwise |
| `FCM_TIMEOUT` | `10s` | one send, and one token exchange |

## Running it on a server

`deploy/compose.prod.yml` runs the images CI published (tagged
`sha-<commit>`, never `latest`) and nothing else; see `deploy/.env.example`
for what it needs. Migrations are forward-only and run on the api's start,
so start the new api once, not two versions side by side. `GET /health`
answers 200 when the process is up; `GET /ready` only when Postgres, Redis
and the logs pool answer too.

A room's invite link lives on its own host (`LINK_PUBLIC_URL`, e.g.
`https://go.gathering.example`). Route that host's `/r/*` and
`/.well-known/*` to the api as a proxy, never a redirect: the phones fetch
`/.well-known/assetlinks.json` and `/.well-known/apple-app-site-association`
from the link host itself and refuse a document behind a redirect. Both are
empty until `APP_LINKS_SHA256` and `APPLE_TEAM_ID` are set (`docs/JOINING.md`).

Push needs `FCM_PROJECT_ID` and `FCM_CREDENTIALS_FILE` naming a key the
api's container can read (a secret mounted as a file, never baked into the
image); without them the api runs and pushes nothing.
