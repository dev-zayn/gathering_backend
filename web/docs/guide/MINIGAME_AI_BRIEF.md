# Gathering minigame brief (for AI models)

You are writing a minigame for **گعدة · Gathering**, a phone app where friends
play short games together in private rooms.
Read this whole brief before you write any code. Every **MUST** is a hard
rule. When the request you were given conflicts with one, keep the rule and
say which one and why.

The full human reference is https://docs.ga3da.fun (the manifest, the bridge,
rooms, your own server, notifications, tournaments). Two working games are
plain pages whose source you can read: https://games.ga3da.fun/xo/
(turn-based, authoritative host) and https://games.ga3da.fun/tap/ (real-time
peers), each with its manifest at `.well-known/gathering-game.json`. This brief
is the part you need to build a game.

## 1. What you are building

- **One web page** (HTML, CSS, JavaScript) that the Gathering app opens full
  screen in a WebView (iOS WKWebView, Android WebView).
- The page talks to the app **only through `window.gathering`** (the
  bridge). The app owns everything else: sign-in, players, rooms and the
  relay between phones, invites, dialogs, sounds, haptics, storage.
- It **plays alone** (practice mode) when opened outside a room, and
  **together** when opened in a room.
- **One page load is one round.** When the host starts the next round, the
  app opens the page again, fresh, on every phone.

## 2. What to hand back

- `index.html`, `style.css`, `app.js` and `.well-known/gathering-game.json`
  (the manifest), each **in full**. No build step unless you were asked for
  one. Any framework is allowed; plain HTML/CSS/JS is preferred.
- Every asset comes from the game's own origin. No CDNs, analytics or
  trackers.
- After the files, the **self-check list** (section 12), each item ticked or
  explained.

## 3. MUST

1. Start with the `bridgeReady()` bootstrap from section 6, **copied as is**,
   and fall back to `browserStub()` so the page also runs in a desktop
   browser. `window.gathering` arrives **after** your scripts start: never
   decide "not in the app" because it is missing at load time.
2. Decide the mode with `await g.room.current()`: `null` means practice (one
   phone, the whole game playable); a room object means multiplayer.
3. In a room, **render every phone from the room's `state` event**, the
   host's phone included. Never trust local state over it.
4. End every round on every phone with `g.celebrate({ kind })` for that
   phone's own outcome (`win`, `lose`, `draw` or `rank`). In a room, **only
   the host** also calls `g.room.end({ scores })`, once, with one entry per
   seated player that **ranks** them: a `rank` (1 is best, equal ranks tie)
   or scores that order them. A tournament counts each round by its ranks,
   so a round where nobody is ranked better counts for nobody.
5. If the manifest says `"splash": true`, call `g.hideSplash()` on **every**
   path, failures included (a `finally` block).
6. Arabic first: `<html lang="ar" dir="rtl">`, Arabic UI words, logical CSS
   properties (`margin-inline-start`, `padding-inline`, `inset-inline-end`),
   never `left`/`right` for layout.
7. Dark first, phone first: one screen, no page-level scroll for the game
   itself, `viewport-fit=cover`, and safe-area padding
   (`env(safe-area-inset-top)` / `-bottom`). With the app's bar hidden an
   iPhone draws the page under the status bar; Android's app stops the page
   under it by itself. The same CSS is right on both.
8. Treat every rejected bridge promise as a **bare code string**
   (`catch (code) { if (code === 'host_only') … }`), never as an `Error`.
9. Keep each `room.send` / `room.setState` payload **under 16 KiB** and
   send **at most 30 frames a second** per player (state writes count).
   Send inputs or deltas, not full snapshots, in real-time games.
10. Write game rules as **pure functions** (`initialState`, `applyMove`
    returning a new state or `null`), so the host and practice mode share
    them.

## 4. MUST NOT

- No `alert`, `confirm` or `prompt` outside the browser stub. They can block
  the WebView. Use `g.showDialog`, `g.prompt`, `g.showActionSheet`.
- No WebSocket, WebRTC, Firebase, Photon or any multiplayer networking of
  your own. The room is the relay.
- No lobby, matchmaking, room codes or invite links of your own. Use
  `g.room.create()` / `g.room.join()` / `g.room.invite()`.
- No login screen, no player names typed by the player: use
  `await g.player.me()`.
- No `localStorage` (use `g.storage`), no `navigator.vibrate` (use
  `g.haptic`), no "Exit" link (use `g.close()`).
- No tokens, secrets or client secrets in the page.
- Don't draw over the app's bar at the top: it holds the game's name, the
  room's code and the app's own controls.
- Don't reset the page for a new round. A new round is a new page load.

## 5. The manifest: `/.well-known/gathering-game.json`

Served from the game's origin, public (a private or localhost address is
refused), as `application/json` (not your `index.html` through a catch-all
route), under 256 KB, answering within 10 seconds.

```json
{
  "id": "yourstudio.game",
  "name": "اسم لعبتك",
  "version": "1.0.0",
  "mode": "embedded",
  "entry": "https://games.yourstudio.example/",
  "orientation": "portrait",
  "splash": true,
  "publisher": { "tier": "partner", "name": "اسم استوديوك" },
  "scopes": ["identity:read"],
  "permissions": [],
  "room": {
    "players": { "min": 2, "max": 8 },
    "host": "authoritative",
    "pace": "turn",
    "state": "blob",
    "lan": true,
    "rejoin": true,
    "spectators": false,
    "chat": true
  }
}
```

| Field | Rule |
| --- | --- |
| `id` | Permanent, lowercase and dotted: `^[a-z0-9]+(\.[a-z0-9-]+)+$` (e.g. `yourstudio.quiz`), up to 64 characters. Also the OAuth `client_id`. |
| `name`, `version` | Required, up to 120 and 32 characters. `name` is the tile's title. |
| `mode` | Always `embedded`. |
| `entry` | The https URL the frame loads; `?code=…` (and sometimes `&path=…`) is appended. |
| `orientation` | `portrait` (default) or `landscape`. |
| `splash` | `true` keeps the app's cover up until `hideSplash()`. |
| `publisher.tier` | `partner`. |
| `publisher.name` | Your studio's name in the store, up to 120 characters. |
| `scopes` | What the game may do with the player: `identity:read` (their name and picture, to your server), `friends:read` (`g.friends.list()` in the page), `notify:send` and `results:write` (from your server). Ask for none you don't use. |
| `permissions` | `camera`, `microphone`, `location`, `notification`, `storage`, `contacts`. Listed ones get the app's question when the game opens, and it won't open if refused; the phone itself asks only at `g.permissions.request(name)`. Prefer `[]` and asking at runtime. Android always refuses `storage`. |
| `redirect_uris` | Only for signing players in from your own website (https://docs.ga3da.fun/auth): where they may be sent back, matched exactly. |
| `room.players` | `min`/`max`, 1 to 64, `max` ≥ `min`. |
| `room.host` | `authoritative` (only the host writes the state) or `peer` (anyone may). |
| `room.pace` | `turn` or `realtime` (shown to players). |
| `room.state` | `blob` (default: a shared, versioned JSON state) or `none` (messages only). |
| `room.lan` | Whether the game may be played on a same-Wi-Fi relay with no internet. Every room plays through the cloud relay today. |
| `room.rejoin` | Whether a member who left may come back while a round is playing. Between rounds anybody who left may. |
| `room.spectators` | Watchers allowed. Reserved: no room holds spectators yet. |
| `room.chat` | The app's chat and emotes over your rooms: `true` when left out. `false` for a game so fast a player can't look away. The page never sees the chat and needs no code for it. |

The tagline, description, icon, screenshots and category are **not** in the
manifest; Gathering's team writes that store copy.

## 6. The bootstrap (copy as is)

```js
function bridgeReady() {
  if (window.gathering && window.gathering.__installed) return Promise.resolve(window.gathering);
  if (!window.GatheringBridge) return Promise.resolve(null); // a plain desktop browser
  return new Promise(function (resolve) {
    var done = false;
    function finish() {
      if (done) return;
      done = true;
      window.removeEventListener('gatheringready', finish);
      resolve(window.gathering && window.gathering.__installed ? window.gathering : null);
    }
    window.addEventListener('gatheringready', finish);
    setTimeout(finish, 10000);
  });
}
```

The browser stub (a desktop stand-in; the only place `alert`/`confirm` may
appear):

```js
function browserStub() {
  const reject = (code) => () => Promise.reject(code);
  const ls = (k) => 'stub.' + k;
  return {
    send() {}, call: reject('unknown_action'),
    showToast: (o) => console.log('[toast]', o && o.title, o && o.subtitle),
    showDialog: (o) => Promise.resolve(confirm([o.title, o.body].filter(Boolean).join('\n'))),
    prompt: (o) => Promise.resolve(window.prompt(o.message || o.title || '', o.initialValue || '')),
    showActionSheet: () => Promise.resolve(null),
    showLoading() {}, hideLoading() {}, showSplash() {}, hideSplash() {},
    setAppBar() {}, showAppBar() {}, hideAppBar() {}, setAppBarColor() {}, setStatusBarColor() {},
    setZoom() {}, setTextSelection() {}, setOrientation() {},
    celebrate: (o) => { alert((o && o.title) || 'انتهت الجولة'); return Promise.resolve(); },
    haptic() {}, playSound() {}, setMuted() {},
    copyToClipboard: (t) => navigator.clipboard && navigator.clipboard.writeText(String(t)),
    getFromClipboard: () => Promise.resolve(null),
    openExternalUrl: (u) => { window.open(u, '_blank'); return Promise.resolve(true); },
    getDeviceInfo: () => Promise.resolve({ platform: 'other', osVersion: '', locale: 'ar-IQ', languageCode: 'ar',
      isDarkMode: true, screenWidth: innerWidth, screenHeight: innerHeight }),
    getAppInfo: () => Promise.resolve({ name: 'browser', version: '0', build: '0', isDebug: true, language: 'ar' }),
    getAuthCode: () => Promise.resolve(null),
    player: { me: () => Promise.resolve({ id: 'local', nickname: 'أنت', handle: 'you', avatar: null, avatarUrl: null }) },
    storage: {
      get: (k) => Promise.resolve(JSON.parse(localStorage.getItem(ls(k)) || 'null')),
      set: (k, v) => Promise.resolve(localStorage.setItem(ls(k), JSON.stringify(v))),
      remove: (k) => Promise.resolve(localStorage.removeItem(ls(k))),
    },
    permissions: { request: () => Promise.resolve('denied'), status: () => Promise.resolve('prompt'),
      openSettings: () => Promise.resolve(false) },
    room: { current: () => Promise.resolve(null), members: () => Promise.resolve([]), send() {},
      getState: reject('not_in_room'), setState: reject('not_in_room'), start: reject('not_in_room'),
      end: reject('not_in_room'), leave: reject('not_in_room'), kick: reject('not_in_room'),
      invite: reject('not_in_room'), ticket: reject('not_in_room'), on() {}, off() {},
      create: reject('offline'), join: reject('offline') },
    friends: { add: reject('not_found'), status: reject('not_found'),
      list: reject('scope_required'), invite: reject('not_in_room') },
    close() {}, minimize() {}, goBack: () => history.back(), reload: () => location.reload(),
    reloadHome: () => location.replace(location.pathname), log: (...a) => console.log('[game]', ...a),
  };
}
```

## 7. The template

Fill in the `TODO`s; keep the structure. Files:

```text
your-game/
├── index.html
├── style.css
├── app.js
└── .well-known/
    └── gathering-game.json   ← the manifest, served as application/json
```

`index.html`:

```html
<!doctype html>
<html lang="ar" dir="rtl">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="color-scheme" content="dark">
  <title>اسم لعبتك</title>
  <link rel="stylesheet" href="style.css">
</head>
<body>
  <main class="screen">
    <header class="top">
      <h1>اسم لعبتك</h1>
      <span id="mode" class="chip">تمرين</span>
    </header>
    <p id="status" class="status" role="status" aria-live="polite"></p>
    <div id="board" class="board"></div>
    <section id="together" class="together" hidden>
      <button id="play-friend" type="button">العب ويا صديق</button>
      <button id="join-room" type="button">انضم لغرفة</button>
      <p id="together-line" role="status" aria-live="polite"></p>
    </section>
  </main>
  <script src="app.js"></script>
</body>
</html>
```

`style.css` (a starting point; make it yours):

```css
:root {
  color-scheme: dark;
  --bg: #0F1222;
  --ink: #F4F5FA;
  --muted: #A9ADC6;
  --key: #FFB300;
  --on-key: #1A1300;
}
* { box-sizing: border-box; }
html, body { margin: 0; height: 100%; background: var(--bg); color: var(--ink); }
body { font-family: system-ui, sans-serif; -webkit-tap-highlight-color: transparent; }
.screen {
  min-height: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: calc(env(safe-area-inset-top) + 12px);
  padding-bottom: calc(env(safe-area-inset-bottom) + 24px);
  padding-inline: 16px;
}
.top { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.status { margin: 0; min-height: 1.5em; color: var(--muted); }
.together { display: flex; flex-wrap: wrap; gap: 8px; margin-block-start: auto; }
button {
  font: inherit;
  border: 0;
  border-radius: 14px;
  padding: 14px 20px;
  background: var(--key);
  color: var(--on-key);
}
button:disabled { opacity: 0.5; }
```

`app.js`, in this order: `bridgeReady()`, `browserStub()` (section 6), then:

```js
'use strict';

let g;               // window.gathering, or the browser stub
let me;              // { id, nickname, handle, avatar, avatarUrl }
let onMove = () => {};

// ---- Your game: pure rules, the same on every phone ----------------------

function initialState(playerIds) {
  return { players: playerIds, turn: playerIds[0], scores: {}, winner: null };
}

// The next state, or null when the move is not allowed (wrong turn, taken cell…).
// Decide `winner` here: a player id, 'draw', or null while the round goes on.
function applyMove(state, playerId, move) {
  if (state.winner || state.turn !== playerId) return null;
  // TODO: apply `move` and return a NEW state object.
  return null;
}

// One line per player that RANKS them: the winner 1, the others 2, a draw all 1.
// A round where nobody is ranked better decides nothing in a tournament.
function scoresFrom(state) {
  return state.players.map((id) => ({
    player: id,
    score: state.scores[id] || 0,
    rank: state.winner === 'draw' || state.winner === id ? 1 : 2,
  }));
}

// Draw the whole screen from `state`, every time. Call onMove(move) from input.
function render(state) {
  // TODO
}

function say(words) {
  document.getElementById('status').textContent = words;
}

(async function main() {
  g = (await bridgeReady()) || browserStub();
  try {
    me = await g.player.me();
    const room = await g.room.current();
    if (room) await enterRoom(room); // together, in a room
    else startPractice();            // one phone, no room
  } catch (code) {
    g.log('start failed:', code);
    say('صار خلل. سكّر اللعبة وافتحها مرة ثانية.');
  } finally {
    g.hideSplash(); // with "splash": true, on every path
  }
})();

// One phone, no room: the whole game, played alone.
function startPractice() {
  document.getElementById('mode').textContent = 'تمرين';
  let state = initialState([me.id]);
  render(state);
  onMove = (move) => {
    const next = applyMove(state, me.id, move);
    if (!next) return g.haptic('selection');
    state = next;
    render(state);
    g.playSound('tap');
    if (state.winner) {
      g.celebrate({ kind: state.winner === 'draw' ? 'draw' : state.winner === me.id ? 'win' : 'lose' });
    }
  };
  offerRoom();
}

// Outside a room, two keys take the player into one for this game.
function offerRoom() {
  const box = document.getElementById('together');
  const line = document.getElementById('together-line');
  const keys = box.querySelectorAll('button');
  box.hidden = false;
  const enter = (ask) => {
    keys.forEach((k) => (k.disabled = true));
    ask().then(
      () => {}, // { id, code }: the room's lobby now takes this page's place
      (code) => {
        keys.forEach((k) => (k.disabled = false));
        if (code !== 'cancelled') line.textContent = wordsFor(code);
      },
    );
  };
  document.getElementById('play-friend').onclick = () => enter(() => g.room.create());
  document.getElementById('join-room').onclick = () => enter(() => g.room.join());
}

function wordsFor(code) {
  return ({
    offline: 'ما في إنترنت. جرّب بعدين.',
    in_another_room: 'إنت بغرفة ثانية.',
    room_full: 'الغرفة مليانة.',
    room_closed: 'الغرفة تسكّرت.',
    invalid_code: 'الرمز مو صحيح.',
  })[code] || 'ما صارت. جرّب مرة ثانية.';
}

// In a room the host referees (manifest: "host": "authoritative", "state": "blob").
// Other phones send moves; the host checks them and writes the state;
// EVERY phone, the host's too, draws from the 'state' event.
async function enterRoom(current) {
  document.getElementById('mode').textContent = 'غرفة ' + current.code;
  let isHost = current.you.role === 'host';
  let version = 0;
  let state = null;
  let celebrated = false;

  const show = (data) => {
    state = data;
    render(state);
    if (state && state.winner && !celebrated) {
      celebrated = true; // each phone celebrates its own outcome, once
      g.celebrate({ kind: state.winner === 'draw' ? 'draw' : state.winner === me.id ? 'win' : 'lose' });
      // The host ends the ROUND; the room stays open for "play again".
      if (isHost) g.room.end({ scores: scoresFrom(state) }).catch((c) => g.log('room.end', c));
    }
  };

  g.room.on('state', (s) => { version = s.version; show(s.data); });
  g.room.on('message', (m) => {
    if (isHost && m.from !== me.id && m.data && m.data.type === 'move') referee(m.from, m.data.move);
  });
  g.room.on('host', async () => { isHost = (await g.room.current()).you.role === 'host'; });

  const saved = await g.room.getState().catch(() => null);
  if (saved && saved.data) {
    version = saved.version;
    show(saved.data);
  } else if (isHost) {
    const seated = (await g.room.members()).filter((m) => m.role !== 'spectator');
    await write(initialState(seated.map((m) => m.id)));
  }

  onMove = (move) => (isHost ? referee(me.id, move) : g.room.send({ type: 'move', move }));

  function referee(playerId, move) {
    if (!state || state.winner) return;
    const next = applyMove(state, playerId, move); // null when illegal
    if (next) write(next);
  }

  async function write(next) {
    try {
      version = await g.room.setState(next, version);
      show(next);
    } catch (code) {
      if (code !== 'state_stale') return g.log('setState', code);
      const s = await g.room.getState(); // someone wrote first: read, then decide
      version = s.version;
      show(s.data);
    }
  }
}
```

For a real-time game with `"host": "peer"`, replace `enterRoom` with this
shape (the host's `room.start()` opens the page on every phone; a page opened
on the start syncs from `startedAt`):

```js
// A race (manifest: "host": "peer", "pace": "realtime"), in place of the
// enterRoom above. Everyone counts from the round's start; each tap is one
// message; the host ends the round, ranked by taps.
let onTap = () => {}; // call onTap() from your tap handler

async function enterRoom(current) {
  const taps = {};
  g.room.on('message', (m) => {
    if (m.from !== me.id && m.data.type === 'tap') taps[m.from] = (taps[m.from] || 0) + 1;
  });
  onTap = () => {
    taps[me.id] = (taps[me.id] || 0) + 1;
    g.room.send({ type: 'tap' }); // ≤ 30 sends a second, each under 16 KiB
  };
  const endsAt = current.startedAt + 10000; // ten seconds, on this phone's clock
  setTimeout(async () => {
    onTap = () => {};
    g.celebrate({ kind: 'rank', title: 'خلص السباق', message: 'نقراتك: ' + (taps[me.id] || 0) });
    if (current.you.role !== 'host') return;
    const seated = (await g.room.members()).filter((m) => m.role !== 'spectator');
    await g.room.end({ scores: seated.map((m) => ({ player: m.id, score: taps[m.id] || 0 })) });
  }, Math.max(0, endsAt - Date.now()));
}
```

## 8. Rooms

A room is a private table: 1–64 players, one host, a relay that delivers every
message **in the same order to every phone**. Outside a room `current()` is
`null`, `members()` is `[]`, `send()` is dropped, `create()`/`join()` take the
player into a room for this game (then the room's lobby replaces the page, so
don't count on running after they resolve), and every other room call rejects
`not_in_room`.

| Call | Returns / notes |
| --- | --- |
| `room.current()` | `{ id, code, game, status, round, you: { role, seat }, config, seq, startedAt, tournament }` or `null`. `status`: `lobby`/`playing`/`ended`; `role`: `host`/`player`/`spectator`; `startedAt`: the round's start in this phone's ms; `tournament`: `null`, or the tournament match this room plays (below). |
| `room.members()` | `[{ id, nickname, handle, avatar, avatarUrl, role, seat, connected }]`, by seat. |
| `room.send(data, { to? })` | Any JSON under 16 KiB (the whole frame), ≤ 30 frames/s; over that the relay drops it silently. `to` targets one player: not echoed to the sender, dropped if they aren't in the room. |
| `room.getState()` | `{ version, data }`; every round starts at `{ version: 0, data: null }`. |
| `room.setState(data, version)` | New version. Pass the version you last saw; rejects `state_stale` if someone wrote first (read again, then decide). Host-only with `authoritative`; `no_state` with `"state": "none"`. |
| `room.start()` / `room.end({ scores })` | Host only. `end` ends the **round**, records results; the room stays. `end` rejects `room_not_started` when no round is playing (the platform may have ended it) and `invalid_fields` for a line naming nobody in the room, a duplicate, or a rank below 1. |
| `room.leave()` / `room.kick(id)` | The player leaves (back to the app's hub) / host removes someone (`tournament_room` in a match room). |
| `room.invite()` | The app's invite sheet; resolves the invited ids (`[]` if closed). |
| `room.ticket()` | `{ token, expiresAt }`, a short-lived ES256 JWT for the game's server. |
| `room.create({ visibility? })` / `room.join({ code? })` | Outside a room only; `{ id, code }`, then the lobby takes the page's place. |

Events, `g.room.on(event, fn)` / `g.room.off(event, fn?)`:

| Event | Detail |
| --- | --- |
| `state` | `{ version, data, by, seq }`; after a reconnect it may be `{ version, data }` alone. Render from this on every phone. |
| `message` | `{ from, seq, data, to? }`. You may get your own back: skip `from === me.id`. |
| `members` | The member list (accept a bare array or `{ list }`). |
| `joined` / `left` | `{ player, seq }` with `player` the whole member / `{ player, seq, reason }` with `player` an id. A dropped connection is not a leave (`connected: false`). |
| `started` / `ended` | `{ at, by, seq, round }` / `{ at, by, seq, reason, round, results }`, once per round. `reason`: `host`, `timeout` (the platform ends a round after two hours), `empty`, `idle`, `admin`, `tournament`, or `''`, a plain end: never score by it. |
| `host` | `{ seq, player, by }`: the host changed; re-check whether you are the host. |
| `kicked`, `connection` | `{ seq, player, by }` / `{ state }`: `connecting`, `open`, `reconnecting` or `closed`, for a "reconnecting…" line. |

Results: `g.room.end({ scores: [{ player: 'ply_1', score: 37, rank: 1 }, …] })`.
`rank` is optional; without it the platform ranks by score, highest first,
ties sharing a place. The app shows the standings in the room's lobby, and the
host's "play again" starts the next round in the same room.

**Tournaments.** A room may be a tournament's match room
(`room.current().tournament` is not `null`). The game needs nothing new: every
round that ends with results is counted, by its ranks. In a knockout a round's
winner is the player ranked strictly better; equal ranks are a draw and the
pair plays again, so a knockout needs `room.players.min` of 2 or less. Only
the match's players get in (anyone else's `room.join` rejects
`not_in_match`), nobody is kicked (`room.kick` and `friends.invite` reject `tournament_room`; the
`room.invite()` sheet's invites are refused and it resolves `[]`), and the
round that decides the match ends with `reason: 'tournament'` (so does a match
decided away from the table, or a tournament called off), and the app shows
the round's results with the way to the tournament. Outside a room, `g.tournament.create()`
and `g.tournament.join({ code? })` start or join one from the game's first
screen, `g.tournament.list()` lists the player's; in a match room
`g.tournament.current()` gives the match and `g.tournament.open()` shows the
bracket or the table (`not_in_tournament` elsewhere). Check that
`g.tournament` exists first: an older app has none. More:
https://docs.ga3da.fun/tournaments.

## 9. Use the bridge, not the browser

| Instead of… | Call |
| --- | --- |
| a loading screen | `"splash": true`, then `g.hideSplash()`; `g.showSplash('…')` re-words it |
| `alert` / `confirm` / a modal | `await g.showDialog({ title, body, confirmText, cancelText })` → `true`/`false` |
| a text popup | `await g.prompt({ title, message, initialValue })` → `string` or `null` |
| a choice menu | `await g.showActionSheet({ title, options: ['أ', { label, value, destructive }] })` → `{ index, value }` or `null` |
| toasts / a spinner | `g.showToast({ title, subtitle })` / `g.showLoading({ message })` … `g.hideLoading()` |
| a results screen | `await g.celebrate({ kind: 'win' \| 'lose' \| 'draw' \| 'rank', title, message })` |
| vibration / sounds | `g.haptic('light' \| 'medium' \| 'heavy' \| 'selection' \| 'vibrate')` / `g.playSound('tap' \| 'tick' \| 'join' \| 'leave' \| 'win' \| 'lose' \| 'countdown')` |
| `localStorage` | `await g.storage.set(key, json)`, `get(key)`, `remove(key)`: per game, wiped on sign-out, small things only |
| a name field / login | `await g.player.me()` → `{ id, nickname, handle, avatar, avatarUrl }` |
| an Exit button | `g.close()`; `g.goBack()` steps back; `g.minimize()` parks the game (not mid-round in a room) |
| fullscreen | `g.hideAppBar()` (keep your own way out that calls `g.close()`: an iPhone has no back key) |
| orientation, bar colour | `g.setOrientation('portrait' \| 'landscape' \| 'auto')`, within what the manifest allows (a portrait game stays portrait); `g.setAppBarColor('#1B1F3A')`, `g.setStatusBarColor(…)` |
| camera / mic / location | `await g.permissions.request('microphone')` → `granted`, `denied`, `permanentlyDenied`…; then the web API. On Android the frame doesn't yet pass the camera, microphone or location to the page's web APIs, and `<input type="file">` opens nothing there: build without them. |
| links, clipboard | `await g.openExternalUrl(url)`; `g.copyToClipboard(text)`, `await g.getFromClipboard()` |
| friends | `g.friends.status(id)`, `g.friends.add(id)` (the app asks the player first), `g.friends.list()` (needs `friends:read`), `g.friends.invite(id)` |
| device and app info, logs | `await g.getDeviceInfo()` (`isDarkMode` is the app's look, not the phone's setting; `languageCode`…), `await g.getAppInfo()`, `g.log(…)` |

## 10. Error codes

```js
try {
  await g.room.start();
} catch (code) {            // a bare string, never an Error
  if (code === 'host_only') g.showToast({ title: 'بس المضيف يبدي' });
}
```

| Code | Meaning |
| --- | --- |
| `not_in_room` | A room call outside a room. |
| `host_only` | A host-only call from someone else. |
| `state_stale` | `setState` against an old version: read again, then decide. |
| `no_state` | `setState` in a room whose manifest says `"state": "none"`. |
| `timeout` | `setState` unanswered for 10 s (a blob over 16 KiB never arrives): read again. Also any platform call the network dropped. |
| `rate_limited` | Over 30 frames a second. |
| `scope_required` | The manifest lacks the scope (e.g. `friends.list` without `friends:read`). |
| `cancelled` | The player closed the app's sheet: say nothing. |
| `offline` / `failed` | No network / failed without a code. |
| `in_room`, `not_a_room_game`, `invalid_code`, `in_another_room`, `room_not_found`, `room_full`, `room_closed`, `room_started`, `another_game`, `friends_only`, `kicked`, `not_found` (the game isn't published), `too_few_players`, `not_member` | `room.create`/`join`/`start` and the platform's refusals: put each in your own Arabic words. |
| `room_not_started`, `invalid_fields` | `room.end` with no round playing / a scores line that names nobody in the room, twice, or ranks below 1; also `room.kick` naming yourself or someone not in the room, and `room.create` with an unknown `visibility`. |
| `tournament_room`, `not_in_match`, `not_in_tournament` | `room.kick` or `friends.invite` in a match room / a join into a match that isn't yours / `tournament.open()` outside a match room. |
| `blocked`, `not_found`, `not_friends`, `already_member` | `friends.add` for someone you blocked / nobody by that id / `friends.invite` for someone not a friend / already in the room. (`friends.add` *resolves* `'cancelled'` when the player says no.) |
| `invalid_orientation`, `unknown_permission`, `not_supported` | A bad orientation word / a permission name outside the list / a call this app doesn't answer yet. |
| `unknown_action` | No such call (a typo, or an older app). |

## 11. Only if the game has its own server

Most games don't need one. With one: the frame loads `entry?code=cod_…`; the
code is valid for **one minute, one use**. The page sends it to the server; the
server trades it at `POST /oauth/token` (`grant_type=authorization_code`, the
`client_id` = manifest `id`, the `client_secret` kept in the server's
environment, shown once at registration), then reads
`GET /v1/bridge/context` with the token. Key data on `player.id`; the phone
number never appears, and the nickname, handle and avatar only with
`identity:read`. Renew with `grant_type=refresh_token`: a renewed token is
`live: false` and carries no room. An `invalid_grant` on renewal means the game
was removed or suspended: stop, don't loop.

- **Room tickets.** For "this player is in this room", the page sends
  `g.room.ticket()`. The server MUST verify the ES256 signature against
  `/.well-known/jwks.json` (the key named by `kid`), then the issuer, the
  audience (`aud` is a list holding your game id), the expiry (ten minutes),
  and that `kind` is `room`: the game token is signed with the same key for
  the same audience, and only `kind` tells them apart.
- **Results.** With `results:write`, `POST /v1/bridge/results`
  (`{ room, round?, scores: [{ player, score, rank? }] }`) replaces that
  round's results whole; a later host `room.end` leaves them as they are, and
  results posted before the round ends count for a tournament match.
- **Notifications.** With `notify:send`, `POST /v1/bridge/notifications`
  (title ≤ 60, body ≤ 160, a `deep_link` path), also pushed to the phone; 10
  per player per game per hour. An `idempotency_key` keeps one notification
  per thing (a repeat still counts toward the limit). A `401`
  (`revoked_token`, `invalid_token`) means stop sending for that player.

The whole of it: https://docs.ga3da.fun/auth, /rooms and /notifications.

## 12. Self-check before you answer

- [ ] `bridgeReady()` is copied as is; `browserStub()` makes the page play in
      a desktop browser (practice mode).
- [ ] `room.current() === null` gives a complete, playable game.
- [ ] In a room every phone renders from `state`; the host alone calls
      `setState` (authoritative) and `room.end({ scores })`, once.
- [ ] Every phone calls `celebrate()` once per round with its own outcome.
- [ ] Every round's scores rank the players (a `rank`, or scores that order
      them): a round where everyone ties decides nothing in a tournament.
- [ ] `hideSplash()` runs on success and on failure.
- [ ] No `alert`/`confirm`/`prompt`, `localStorage`, `navigator.vibrate`,
      sockets or secrets outside the stub.
- [ ] RTL with logical properties; nothing under the notch or the home
      indicator; one phone screen.
- [ ] Payloads under 16 KiB, at most 30 sends a second.
- [ ] Rejections handled as bare code strings; every refusal the player can
      meet has Arabic words.
- [ ] The manifest validates against section 5 and is served as
      `200 application/json`.
