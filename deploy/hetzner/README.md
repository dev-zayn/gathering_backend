# ga3da.fun on Hetzner

The whole platform on one Hetzner Cloud server: Caddy (HTTPS), the api, the
console, the docs, the games, Postgres and Redis, built on the server from
the source `deploy.sh` sends it. Every secret is made on the server and
stays there (`/opt/ga3da/secrets`).

| Address | What answers |
| --- | --- |
| `https://api.ga3da.fun` | the api: the app, the relay's sockets, `/files/*` (but not the console's routes) |
| `https://console.ga3da.fun` | the console: its own sign-in, any name and the console password |
| `https://docs.ga3da.fun` | the developer docs (the app section behind `DOCS_USER`), and the minigame guide at `/guide/` |
| `https://games.ga3da.fun/<game>/` | every game in `../gathering_games` |
| `https://ga3da.fun/r/<code>`, `/t/<code>` | invite links, and the phones' `/.well-known/` documents |
| `https://ga3da.fun/` | the landing page (`site/index.html`, its pictures in `site/img/`, Arabic) |
| `https://ga3da.fun/privacy` | the privacy policy the store listings link to (`site/privacy.html`, Arabic then English; `#delete` is how to delete an account) |

## For now

- **One sign-in code for every phone number** (`OTP_FIXED_CODE`, made at
  random on the first deploy and printed once). Anyone who has it can sign in
  as any number: give it to testers only. This needs the api's debug mode,
  which also logs every SQL statement.
- **The console signs in with one password** and the name you type
  (`CONSOLE_PASSWORD_HASH`), not a provider. The api's host refuses the
  console's routes; only `console.ga3da.fun` answers them.
- **Pictures on the server's disk** (`/opt/ga3da/data/files`), not a bucket.
- **Backups:** switch on the server's Backups in the Hetzner console (daily
  snapshots of the whole disk, the database included).

Retiring them: an SMS or WhatsApp gateway (`OTP_WEBHOOK_URL`,
`OTP_WEBHOOK_TOKEN`), a sign-in provider for the console (`SSO_*`, and the
console built with `VITE_SSO_*`), a bucket (`S3_BUCKET`, and for Hetzner's
Object Storage `AWS_ENDPOINT_URL_S3`), then `DEBUG`, `OTP_FIXED_CODE` and
`FILES_DIR` out of `compose.yml`.

## The first time

1. **A server.** Hetzner Cloud: Ubuntu 24.04, Falkenstein, Nuremberg or
   Helsinki (the closest to Iraq), 2 GB or more (x86 or Arm; setup.sh adds
   swap so the builds fit), your SSH key. Rescale later with the server off,
   "CPU and RAM only" to keep the way back down. Its firewall: TCP 22, 80 and 443, UDP 443.
2. **DNS.** At the domain's registrar, an A record to the server's IPv4 for
   each of `ga3da.fun`, `api`, `console`, `docs` and `games`. Caddy asks for
   the certificates once they resolve.
3. **Deploy** from the Mac, in this repo. `FIREBASE_KEY` is the Firebase
   service account's JSON key (the file `FCM_CREDENTIALS_FILE` names in
   `backend/.env`); without it the api runs and pushes nothing.

       FIREBASE_KEY=/path/to/key.json deploy/hetzner/deploy.sh root@<server-ip>

   The first run installs Docker, makes the secrets and prints the console's
   password, the docs' and the sign-in code: keep them. It ends registering
   and publishing every game; one with no icon stays unpublished until the
   console gives it one (Games, the game, Icon), then Publish.
4. **The app**, built against the server:

       --dart-define=GATHERING_BASE_URL_PROD=https://api.ga3da.fun
       --dart-define=GATHERING_LINK_HOST=ga3da.fun

   and `link_host.xcconfig` set to `ga3da.fun` too.

## After that

The same command deploys every change: `deploy/hetzner/deploy.sh root@<ip>`.
It builds the arcade games, syncs, rebuilds what changed and restarts it;
the database migrates itself on the api's start. It sends the Mac's tree as it
is, committed or not, and every game folder, and republishes every game.

### One game alone

To put out one game and touch nothing else (how انقلاب and طابو went out),
check it, then send its folder alone. The games server reads the folder as it
is, with no restart:

    (cd ../gathering_games/<game> && bun run build && bun run lint && bun run test)
    rsync -az --exclude node_modules ../gathering_games/<game>/ root@<ip>:/opt/ga3da/gathering_games/<game>/

A game already in the store is updated with that. A new one is added in the
console (Games): Register by URL `https://games.ga3da.fun/<game>/`, then its
Icon (`art/icon.png`), its store copy, and Publish. The registry reads a
changed manifest again about once a day, or at once with the game's "Re-read it
now"; a manifest asking for more scopes waits there until you accept it.

Mind the app on the phones: a game that hides the app's bar in play needs build
6 or later, which stops the frame under Android's status bar. On older builds the
game draws under the clock.

### The docs alone

The docs (`web/docs`, and the minigame guide in `web/docs/guide/`, which
`python3 build.py` in `../gathering_docs` writes) are one image. Build and
check them, send that folder, then rebuild only that service:

    (cd web/docs && npm run build && npm run lint)
    rsync -az --delete --exclude node_modules --exclude dist --exclude dist-app web/docs/ root@<ip>:/opt/ga3da/gathering/web/docs/
    ssh root@<ip> "cd /opt/ga3da/gathering/deploy/hetzner && BUILD_COMMIT=$(git rev-parse --short HEAD) docker compose --env-file /opt/ga3da/secrets/ga3da.env build docs &&
      docker compose --env-file /opt/ga3da/secrets/ga3da.env up -d docs"

The console goes out the same way, with `web/console` and the service
`console`.

Each docs build also writes `llms.txt`, `llms-full.txt` and a `.md` beside
every page, for AI models (`web/docs/mdx/llms.mjs`).

### The docs' MCP server

`backend/cmd/mcp`, the service `mcp`, answers at `https://docs.<domain>/mcp`
(Caddy sends `/mcp` there, everything else to the docs). It reads the docs from
the `docs` service on the inside, so a docs deploy is all it needs to say
something new; it is rebuilt only when its own code changes. It holds no
secret and no database. Send `backend/` and this folder, build it, start it,
then reload Caddy for a changed Caddyfile:

    rsync -az --delete --exclude .env --exclude .files --exclude /bin backend/ root@<ip>:/opt/ga3da/gathering/backend/
    rsync -az deploy/hetzner/ root@<ip>:/opt/ga3da/gathering/deploy/hetzner/
    ssh root@<ip> "cd /opt/ga3da/gathering/deploy/hetzner && docker compose --env-file /opt/ga3da/secrets/ga3da.env build mcp &&
      docker compose --env-file /opt/ga3da/secrets/ga3da.env up -d mcp &&
      docker compose --env-file /opt/ga3da/secrets/ga3da.env exec -T caddy caddy reload --config /etc/caddy/ga3da/Caddyfile --adapter caddyfile"

Check it with a tool call (`RATE_PER_MINUTE`, 60 by default, limits each address):

    curl -s https://docs.ga3da.fun/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
      -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_manifest","arguments":{"url":"https://games.ga3da.fun/xo/"}}}'

### Which build is live

The console and the docs each say which build they are: the version in their
`package.json` and the commit they were built from, in the console's rail and
on its sign-in page, and in the docs' footer. Each also answers it, with when
it was built (`web/console/version.mjs`, and the same file in `web/docs`):

    curl https://console.ga3da.fun/version.json
    curl https://docs.ga3da.fun/version.json          # the app section: /app/version.json, behind the password

The image is built on the server from a copy with no `.git`, so the commit
comes from `BUILD_COMMIT` when it is built: `deploy.sh` sets it (with a `+`
when the tree it sent had changes not yet committed), and the commands above
do. Built without it, they name their version alone. Raise `version` in the
`package.json` when a release is worth a name of its own; the commit already
names every build.

On the server (`ssh root@<ip>`):

    cd /opt/ga3da/gathering/deploy/hetzner
    docker compose --env-file /opt/ga3da/secrets/ga3da.env ps
    docker compose --env-file /opt/ga3da/secrets/ga3da.env logs -f api
    cat /opt/ga3da/secrets/ga3da.env           # the sign-in code, the docs' password
    cat /opt/ga3da/secrets/console.password    # the console's
