# Gathering (جمعة)

The minigame super app's platform: one sign-in, one player profile, friends,
rooms, and the games as HTML mini apps inside the app. This file is for
whoever runs it. What the code does is in the code; what a game developer
needs is in `web/docs`.

## What is in the repository

    backend/    the Go module: two commands (api, fixtures) and the e2e sweep
    web/console the admin console, React, static files behind nginx
    web/docs    the developer docs, static files behind nginx
    deploy/     the Dockerfile for the Go binary, compose for a laptop and a server,
                the Postgres init, the fixture games
    scripts/    brand-check (no borrowed brand identifier may land here)

Three things run: the **api** (8080), which also runs its own clock to tidy
its tables and end rooms nobody is in, and two static sites, the **console**
(8085) and the **docs** (8092). They need Postgres, Redis and somewhere to put
pictures (S3, or a directory on a laptop). Admins sign in through any OIDC
provider; on a laptop `DEBUG=true` + `DEV_ADMIN=true` opens the console without one.

## Running it on a laptop

You need Docker, `make` and `openssl`. Go and Node are only needed to run
things outside Docker.

    make env        # writes backend/.env from the example with a fresh signing key
    make up         # postgres, redis, api, console, docs, fixtures
    make logs       # the api; make logs S=console for another

- Console at http://localhost:8085 (dev sign-in), docs at http://localhost:8092
  (user/password `gathering`), API at http://localhost:8080 (`/docs` for the
  OpenAPI UI).
- `DEBUG=true` means no code is sent to a phone; it is in the api log:

      docker compose -f deploy/compose.yml logs api | grep "OTP NOT SENT" | tail -1

- The fixture games are served at `http://fixtures:8091/xo` and
  `http://fixtures:8091/tap` (`http://localhost:8091/…` from the laptop).
  Register either from the console, Games, by that URL. The same container
  runs a tiny partner backend under `/partner/`: `GET /partner/entry?code=`
  shows a launch code arrived, and `POST /partner/exchange {code}` spends it
  at the api with `PARTNER_CLIENT_ID`/`PARTNER_CLIENT_SECRET` (the secret the
  console showed when `fixture.xo` was registered) and reads the bridge.

`make check` runs what CI runs: brand-check, gofmt, tidy, vet, the Go tests
with the race detector, and the console and docs builds. The Go tests need
`TEST_DATABASE_URL` (and `TEST_REDIS_URL`), or every database test skips and
reports ok. `make e2e` runs the sweep against a running api: with
`OTP_FIXED_CODE=123456` in the api's env every sign-in uses that code, and
`DEV_ADMIN=true` lets the sweep open the console. The sweep expects, in
order: two players sign up, edit their records, find each other, rotate a
session and sign out; the console signs in and reads the dashboard, the
players and the log; a game served by the test itself is registered by URL,
given its store copy and published; a player sees it in the store, opens it
(the launch mint puts it on the shelf), the test spends the code as the
game's server at `/oauth/token`, reads `/v1/bridge/context` and
`/oauth/userinfo`, renews, signs in with PKCE through `/oauth/authorize`,
then takes the game off the shelf and sees every token refused; the console
suspends, republishes and features it. The api must run with
`ALLOW_PRIVATE_HOSTS=true`, since the test's server is on loopback (set
`E2E_SELF=host.docker.internal` for an api in a container). With
`E2E_FIXTURES=http://localhost:8091` the sweep also registers the fixture
`xo` and has the fixtures' partner backend spend a code, which needs
`PARTNER_CLIENT_SECRET` on the fixtures to be that game's secret.

## Running it on a server

`deploy/compose.prod.yml` runs the images CI published and nothing else; see
`deploy/.env.example` for what it needs. Migrations are forward-only and run on
the api's start, so start the new api once, not two versions side by side.
`GET /health` answers 200 when the process is up; `GET /ready` only when
Postgres, Redis and the logs pool answer too.
