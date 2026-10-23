# Joining without typing (M7)

The contract for the three ways into a room besides typing its code: the
room's **invite link**, its **QR code**, and **nearby rooms** on the same
Wi-Fi. The platform (`backend/internal/http/guest`, `conf`) and the app
(`gathering_app`) both build to it; when they disagree this file decides,
and when it is wrong it is fixed here first.

Every way ends in the same call, `POST /v1/rooms/join {code}`, with every
rule of docs/ROOMS.md "Joining": none of them admits anybody the typed code
would not. Phase 1 still plays every room through the cloud relay; nearby
only saves typing the code (the LAN relay is Phase 2).

## The invite link

Every Room already answers `link`: `LINK_PUBLIC_URL/r/{code}`. In production
`LINK_PUBLIC_URL` is the link host (`https://go.gathering.example` until the
real one is known); on a laptop it is the api itself (`http://localhost:8080`).
A link host other than the api routes `/r/*` and `/.well-known/*` to the api,
as a proxy and never a redirect: the phones refuse a document behind one.

### `GET /r/{code}` (operation `invite-link`, public)

An HTML page for whoever opens the link where the app does not catch it: a
phone without the app, a desktop, a link the OS did not verify.

- `{code}` is read like a typed code (case-insensitive; spaces and dashes
  ignored). It answers **200** when an open room (`lobby` or `playing`, a
  room waiting between rounds included) holds the code, else **404** with
  the same page in its "not found" form: no open room holds it, the room
  that held it has ended, or it cannot be a code at all (not six symbols of
  the alphabet). A failure on our side (the database out of reach) is
  **500** with the page's "try again" form. Never a JSON error: this is a
  page.
- The page, Arabic first (`lang="ar" dir="rtl"`), English (`lang="en"
  dir="ltr"`) when `Accept-Language` prefers `en` over `ar`: its best `en`
  tag (`en`, `en-GB`, …) weighs more than its best `ar` tag, or as much and
  comes first. Only `en` and `ar` tags count (`*` counts for neither), and
  `q=0` refuses a language; no header, or neither language named, is Arabic.
  - the game's name and icon (the signed icon link, or none), the code large,
    in an LTR island, with a copy key ("انسخ الرمز", then "انّسخ الرمز";
    "Copy the code", then "Code copied"). The key is the script's to show,
    so a browser that runs no script shows none; the code selects whole;
  - "عندك گعدة؟ افتحها واكتب الرمز" and its English line, "Have Gathering?
    Open it and type the code.";
  - a store key per configured store (`APP_STORE_URL` "App Store",
    `PLAY_STORE_URL` "Google Play") under "ما عندك گعدة؟ نزّلها" ("Don't
    have Gathering yet?"), none and no line when neither is set;
  - not found: "هالغرفة ما موجودة، أو خلصت" and its English line, "This
    room doesn't exist, or it's over.": no game, no code, no copy key, no
    "open it" line; the store keys stay;
  - try again: "ما كدرنا نفتح الرابط هسّه. جرّب بعد شوية." ("We couldn't
    open this link just now. Try again in a moment."), the store keys too.
- The title is "انضم لغرفة {game} · گعدة" ("Join a {game} room ·
  Gathering"), and "گعدة" ("Gathering") on the other forms. `<meta
  name="robots" content="noindex">` keeps codes out of search engines.
- It shows nothing about the room beyond its game and code: no host, no
  members, no visibility. A friends-only room's page looks like any other;
  the join still refuses a stranger.
- Headers, on every form: `Cache-Control: no-store`; `Content-Type:
  text/html; charset=utf-8`; `Vary: Accept-Language`; `Referrer-Policy:
  no-referrer` (a tap on a store key does not carry the link, and its code,
  to the store); `X-Content-Type-Options: nosniff`; and a strict
  `Content-Security-Policy`: `default-src 'none'; script-src 'sha256-…';
  style-src 'sha256-…'; img-src <files origin>; base-uri 'none';
  form-action 'none'; frame-ancestors 'none'`. The copy key's script and
  the page's one style are inline, allowed by their hashes; a form without
  the copy key has `script-src 'none'`. The files origin is the icon link's
  own (scheme, host and port: the bucket, or the api's `/files/` on a
  laptop), and `img-src 'none'` when there is no icon. The colours are the
  app's dark set, Arena (`gathering_app/DESIGN.md`, beside this
  repository); no web fonts are required.

### The two documents the phones check

- `GET /.well-known/assetlinks.json` (operation `assetlinks`, public,
  `application/json`): `[{"relation":
  ["delegate_permission/common.handle_all_urls"], "target": {"namespace":
  "android_app", "package_name": APP_ANDROID_PACKAGE,
  "sha256_cert_fingerprints": [APP_LINKS_SHA256…]}}]` (every fingerprint, in
  the order configured), or `[]` while `APP_LINKS_SHA256` is empty.
- `GET /.well-known/apple-app-site-association` (operation
  `apple-app-site-association`, public, `application/json`, no extension):
  `{"applinks": {"details": [{"appIDs": ["APPLE_TEAM_ID.APP_IOS_BUNDLE"],
  "components": [{"/": "/r/*"}, {"/": "/t/*"}]}]}}`, or `{"applinks":
  {"details": []}}` while `APPLE_TEAM_ID` is empty. `/t/*` is a
  tournament's link (docs/TOURNAMENTS.md, "The link": `GET /t/{code}`,
  operation `tournament-link`, this page in a tournament's words).
- Both `Cache-Control: max-age=3600`. Neither is behind a redirect.

### Configuration

`LINK_PUBLIC_URL`, `APP_LINKS_SHA256` (comma list) and `APPLE_TEAM_ID`
exist. New, in `conf` and `.env.example`: `APP_ANDROID_PACKAGE` and
`APP_IOS_BUNDLE` (both default `app.gathering.jamaa`), `APP_STORE_URL` and
`PLAY_STORE_URL` (empty by default). They replace M0's placeholders
`APP_BUNDLE_ID`, `STORE_URL_IOS` and `STORE_URL_ANDROID`, which nothing
read. Real values arrive with the real identifiers; placeholders until then.

### The app catches the link

- Android: an intent filter (`VIEW`, `DEFAULT`, `BROWSABLE`, `autoVerify`)
  for `https://<link host>/r/*` and `/t/*` (a tournament's link); the debug
  build also takes `http://localhost:8080/r/*` and `/t/*`, the laptop's. iOS: Associated Domains
  `applinks:<link host>`. The link host is build configuration (one value
  for both platforms, `go.gathering.example` until the real one).
- `app_links` hands the app the link at a cold start and while it runs. Any
  URL whose path is `/r/{code}` counts, whatever its host (so a laptop's or a
  shared link works); the code is read like a typed code.
- Signed in: the Join sheet opens with the code filled in, and one tap joins.
  The room the player is already in opens its lobby instead. A player still
  in another open room meets `in_another_room` and its way back, as on any
  join. Signed out: the link waits, and is honoured once, right after the
  sign-in lands on the hub.

## The QR code

- The lobby's code card gains a QR key: a sheet with the room's `link` as a
  QR code, large, and the code under it. Any camera opens it: the app where
  the link is caught, the page otherwise. It is offered whenever the room
  waits in its lobby, between rounds too.
- The Join sheet gains "امسح QR": a scanner page (`mobile_scanner`) that
  takes the first code it reads that is either a URL whose path is
  `/r/{code}` (any host) or a bare six-symbol code, and joins with it; a QR
  that is neither says "هذا مو رمز غرفة" and keeps scanning.
- The camera permission is asked through the app's own permission flow when
  the scanner opens; denied, the page says so, offers the settings, and the
  code field is one tap away.

## Nearby rooms (the same Wi-Fi)

Discovery only: it finds the code of a room hosted on a phone nearby.

- **Package:** `nsd` (5.x; `bonsoir` if `nsd` blocks).
- **Who advertises:** a phone whose player hosts a room that waits in its
  lobby (the first round or between rounds), whose visibility is `private`,
  while the app is in the foreground. Friends-only rooms are never
  advertised: the people they admit can be invited. A phone stops
  advertising on a start, a leave, a hand-over (the new host's phone takes
  over), a room's end, the visibility not `private`, and the app leaving the
  foreground; it advertises again when the room is back in its lobby.
- **The service:** type `_gathering._tcp`; name `گعدة <code>`; port `4747`
  (nothing listens there in Phase 1: Android refuses port 0, and Phase 2's
  LAN relay will put its real port here); TXT, every value UTF-8:
  - `v=1`
  - `code=<code>`
  - `game=<game id>`
  - `host=<the host's nickname, cut to 40 bytes on a character boundary>`
  - `n=<current members>`, `max=<config.max>`
  - `round=<round>`

  A change of `n`, `round` or `host` updates the record (unregister and
  register again where the package cannot update TXT in place).
- **Who discovers:** the Join sheet, while it is open and the app is in the
  foreground. It browses `_gathering._tcp`, resolves each service, and keeps
  the rooms whose TXT reads `v=1` with a well-formed `code` and `game`; it
  drops the player's own room and rooms whose `n` is at `max` (full), and
  merges each with the games registry for the icon and the name (a game the
  store does not list shows the TXT's id and no icon). The list, "قريب منك":
  icon, game, host, `n/max`; one tap joins by its code. A room that vanished
  from the network leaves the list.
- **The Join sheet** says, before the OS asks on iOS, why it wants the local
  network ("حتى نلقى الغرف القريبة منك بنفس الواي فاي"). Nothing found yet:
  "ما لقينا غرف قريبة بعد" with a quiet searching state, never an error.
  Denied: the list says so and the code field stays.
- **Native keys.** iOS: `NSBonjourServices` `[_gathering._tcp]`,
  `NSLocalNetworkUsageDescription` (Arabic and English through
  `InfoPlist.strings`). Android: `CHANGE_WIFI_MULTICAST_STATE`; no location.

## Also in M7

`Permission.storage` is always denied on Android 13 and later: the app's
storage permission maps to the photos permission there (by the SDK version),
and stays `storage` below.

## Proofs

- **Platform:** the link page (200 for an open room, a room between rounds
  included; 404 for none and for an ended one; the code read like a typed
  one; Arabic by default, English by `Accept-Language`; the headers), the
  two documents (empty and configured), the routes public in
  `guarded_test.go`, the docs (a short "Invite links" section on the Rooms
  page, the deploy note on the link host), and an e2e step that reads a
  room's link page and both documents.
- **App:** the TXT written and read (including a cut nickname and bad
  records), the list merged and filtered, the advertising's life (lobby,
  start, back to the lobby between rounds, leave, hand-over, background,
  friends-only), the QR's payload read (link, bare code, anything else), the
  link handled at a cold start, while running, and signed out, the lobby's
  QR sheet, the Android 13 mapping.
- **Devices** (the user's own emulator and simulator are never used):
  - nearby: two iOS simulators made for the proof (they share the Mac's
    network, so Bonjour works between them; Android emulators before 36.5
    cannot see each other): the host's lobby advertises, the guest's Join
    sheet lists it, and one tap joins;
  - the link: on two Android emulators, the guest's phone opens the room's
    link (an `adb` `VIEW` intent for the app's package) and joins from the
    filled-in sheet.
