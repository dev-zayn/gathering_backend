# Push (2026-10-22)

The contract for push notifications through Firebase Cloud Messaging, now
that the Firebase project exists: project **`gathering-supergame`** (sender
`928600185877`), the Android app and the iOS app both `app.gathering.jamaa`.
The platform (`backend/internal`) and the app (`gathering_app`) build to it;
when they disagree this file decides, and when it is wrong it is fixed here
first.

Push is a second way to hear the inbox: every notification the inbox gains
is also pushed to the recipient's phones. Nothing is pushed that is not in
the inbox, and the inbox stays the record (a push missed is a row still
there).

## The phone's address (exists since M1)

The address is the phone's **Firebase installation id (FID)**: the app calls
firebase_messaging's `register()`, and `onRegistered` hands out the FID, which
the platform sends to as FCM's `message.fid`. The older registration token
(`getToken()`, deprecated in firebase_messaging 17) is not used: with this
SDK a token was handed out without the app instance being registered, and
FCM answered every send to one `UNREGISTERED`. The platform still sends an
address that is not an FID (22 URL-safe symbols, the first c to f) as
`message.token`, which FCM takes for now. The route's field keeps its name,
`token`. `register()` needs the installation id turned on (Android
`firebase_messaging_installation_id_enabled`, iOS
`FirebaseMessagingInstallationIdEnabled`): without it the SDK refuses with
"API disabled".

Firebase's automatic registration at start is **off** (Android
`firebase_messaging_auto_init_enabled` and
`firebase_analytics_collection_enabled` false, iOS
`FirebaseMessagingAutoInitEnabled` NO): an install is registered only when
the app calls `register()`, once the player has allowed notifications. Two
installs that look like one device (emulators copied from one snapshot share
their device id) otherwise replace each other's registration at start, and
FCM answers sends to the replaced one `UNREGISTERED`.

- `PUT /v1/me/device {token, platform}` (`platform` `ios` or `android`):
  this session's phone and its address (`token`: the FID, or an older
  registration token). One phone per session; the same address sent from
  another session moves there.
- `DELETE /v1/me/device`: this session's phone stops getting pushes.
- A session that ends (sign-out, revoked, expired) no longer gets pushes:
  the platform skips its device row (a push reaches only phones on live
  sessions of players not switched off), and the clock's sweep deletes the
  row within a minute.

## When the platform pushes

After a notification row is written and its transaction has committed,
whatever raised it:

- `friend_request`, `friend_accepted` (docs/FRIENDS.md),
- `room_invite` (an invite, or a repeated one: see collapsing),
- `game` (a game's server, `POST /v1/bridge/notifications`),
- `system` (an announcement from the console, docs/CONSOLE.md: one push per
  recipient's phone),
- `tournament` (docs/TOURNAMENTS.md, "Notifications": an invite, the start,
  a match ready, the end, the cancellation).

A raise that updates an existing row in place (the key upsert) pushes again.
A disabled player gets nothing.

## The message (FCM HTTP v1)

`POST https://fcm.googleapis.com/v1/projects/{FCM_PROJECT_ID}/messages:send`
with an OAuth 2 token from the service account (scope
`https://www.googleapis.com/auth/firebase.messaging`), one call per token:

```json
{ "message": {
    "fid": "<the phone's FID>",
    "notification": { "title": "<the row's title>", "body": "<the row's body>" },
    "data": { "notification": "ntf_…", "kind": "room_invite",
              "deep_link": "/r/K7M2QX", "room": "room_…", "code": "K7M2QX",
              "game": "fixture.xo", "player": "ply_…", "announcement": "ann_…",
              "tournament": "trn_…", "match": "trm_…" },
    "android": { "priority": "HIGH",
                 "notification": { "channel_id": "inbox", "tag": "<collapse>" } },
    "apns": { "headers": { "apns-collapse-id": "<collapse>" },
              "payload": { "aps": { "sound": "default", "badge": <unread> } } } } }
```

- The title and body are the inbox row's own, in the language they were
  written in.
- `data` carries strings only, and only the keys that apply to the kind:
  `notification` and `kind` always; then `deep_link`, `room`, `code`,
  `game`, `player`, `announcement`, `tournament`, `match` as the row has
  them.
- **Collapse:** the row's key when the platform wrote it
  (`room_invite:{room}`, `friend_request:{player}`,
  `friend_accepted:{player}`, `announcement:{id}`, `tournament_match:{match}`
  and the tournament's own keys), else the row's id, cut
  to 64 bytes (APNs' limit) at a character. A repeated invite replaces the
  earlier push on the phone instead of piling up. A game's own
  notification (`game`) collapses on its id, not its key: a game's keys are
  its own, per player and game, so two games (or a game and the platform)
  can hold the same key, and a long Arabic key cut to 64 bytes can meet
  another; the id is what the key's upsert keeps, so a repeated key still
  replaces its push.
- **Badge:** the recipient's unread count after the write.

## Delivery

- Asynchronous, after the commit: a raise never waits for Firebase (a full
  queue drops the push with a log line), and a failure never undoes a
  notification. On shutdown the api sends what is queued for up to five
  seconds and abandons the rest.
- Bounded concurrency (8 sends at a time per process); an announcement to
  many players goes through the same pool.
- A token Firebase rejects as gone (`UNREGISTERED`, or a 404 for the
  token, or `INVALID_ARGUMENT` naming the token) deletes that device row.
  A 429 or 5xx is retried once after a short backoff. Anything else is
  logged with the device id (never the token) and dropped. Every send
  Firebase accepts is logged as `push sent` with the notification and
  device ids: from there, delivery is Firebase's.
- **Configuration:** `FCM_PROJECT_ID` (`gathering-supergame`) and
  `FCM_CREDENTIALS_FILE` (the path to a Firebase service-account JSON key,
  kept outside the repo, never committed; a key without a project refuses
  to start). `FCM_ENDPOINT` overrides the base URL, for tests;
  `FCM_TIMEOUT` (10s) bounds one send. Without credentials, every push is
  logged as "would push" with the notification id and the device count,
  and nothing is sent: a laptop needs no key. The line, in the api's JSON
  log: `"msg":"would push","notification":"ntf_…","devices":1` (with
  `LOG_FORMAT=text`: `would push notification=ntf_… devices=1`); a player
  switched off gets no line.

## The app

- **Firebase:** `firebase_core` and `firebase_messaging`, initialised from
  `lib/firebase_options.dart` before the app runs (Android and iOS only;
  tests run without Firebase behind a `PushService` with a fake).
- **Asking:** the app asks to send notifications at a moment that explains
  itself. The first time the hub shows after sign-in, a sheet in its own
  words says what pushes are for ("نبهك لمن يدزلك صديق طلب أو دعوة للعب"),
  and only its "تمام" makes the system ask (iOS; Android 13 and later).
  "مو هسه" asks again no sooner than a week later. Already granted, nothing
  shows.
- **The address:** once permission is granted, the app registers the
  install (`register()`) and sends the FID it is handed with `PUT
  /v1/me/device`; again when Firebase hands out a new one, and on every
  sign-in. A failed registration or send is tried again by itself after
  15, 30 and 60 seconds, then on the next return to the foreground (a
  phone that has just started often fails its first). Sign-out sends
  `DELETE /v1/me/device` and unregisters the install (`unregister()`).
- **In the foreground:** a push shows as the app's own toast (the title,
  and the body when short), the inbox's badge and list are read again, and
  tapping the toast does what tapping the inbox row does.
- **From the tray** (the app in the background or not running): tapping the
  notification opens the app where the inbox row would. A request or an
  accept opens the player's card, an invite joins the room by its code and
  opens its lobby, a game's opens the game at its link, and an announcement
  opens its game's store page or else the inbox. It is marked read.
- **Android:** the `inbox` channel ("الإشعارات" / "Notifications") is made
  by the app and set as Firebase's default channel; `POST_NOTIFICATIONS` is
  declared.
- **iOS:** `UIBackgroundModes` `remote-notification`, and the
  `aps-environment` entitlement. Delivery on iOS also needs the APNs key
  uploaded to the Firebase project and the Push capability on the App ID
  (the user's, in the Apple and Firebase consoles).

## Proofs

- Platform: the sender against a fake FCM server (the message's shape per
  kind, the address as `fid` or `token` by its shape, collapse, badge, the
  token deleted on `UNREGISTERED`, the retry once, the "would push" log
  without credentials, concurrency held), a push for each raising path, a
  disabled player and an ended session skipped; an e2e step that
  registers a device and sees the platform try to push (through
  `FCM_ENDPOINT` pointed at the sweep's own fake, `E2E_FCM`; without it
  the step checks the device and the inbox row).
- App: the permission sheet (asked once, "مو هسه" waits a week), the token
  sent, refreshed, tried again after a failure and removed on sign-out,
  the foreground toast, and the tray tap routed for every kind.
- Device (`E2E_TEST=push`): one Android emulator, booted cold (a snapshot's
  clock is a day behind, and FCM answers sends to what such a phone
  registered `UNREGISTERED`); the host is a player the script plays on the
  api, since emulators copied from one AVD share a device id and a second
  one's registration gets in the way. The guest grants notifications and
  its FID reaches the platform; the host's friend request reaches the
  guest's phone, first in the foreground (the toast) and then, asked
  again with the app in the background, in the system's list on the
  `inbox` channel. Shown 2026-10-22 through `gathering-supergame`: the
  api logged `push sent` for both.
