# ga3da.fun on Hetzner

The whole platform on one Hetzner Cloud server: Caddy (HTTPS), the api, the
console, the docs, the games, Postgres and Redis, built on the server from
the source `deploy.sh` sends it. Every secret is made on the server and
stays there (`/opt/ga3da/secrets`).

| Address | What answers |
| --- | --- |
| `https://api.ga3da.fun` | the api: the app, the relay's sockets, `/files/*` (but not the console's routes) |
| `https://console.ga3da.fun` | the console: its own sign-in, any name and the console password |
| `https://docs.ga3da.fun` | the developer docs (the app section behind `DOCS_USER`) |
| `https://games.ga3da.fun/<game>/` | every game in `../gathering_games` |
| `https://ga3da.fun/r/<code>`, `/t/<code>` | invite links, and the phones' `/.well-known/` documents |

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
the database migrates itself on the api's start.

On the server (`ssh root@<ip>`):

    cd /opt/ga3da/gathering/deploy/hetzner
    docker compose --env-file /opt/ga3da/secrets/ga3da.env ps
    docker compose --env-file /opt/ga3da/secrets/ga3da.env logs -f api
    cat /opt/ga3da/secrets/ga3da.env           # the sign-in code, the docs' password
    cat /opt/ga3da/secrets/console.password    # the console's
