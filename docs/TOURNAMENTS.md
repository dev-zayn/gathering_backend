# Tournaments

The contract the platform (`backend/internal/biz/tournaments`, the rooms'
match door and count in `biz/rooms`, `http/player`, `http/admin`,
`http/guest`) and the app (`gathering_app/lib/modules/gathering/…/
tournaments`) both build to. When the two disagree, this file is the
tie-breaker; when this file is wrong, fix it here first. It amends
docs/ROOMS.md where a room plays a tournament's match (*Match rooms*), and
docs/FRIENDS.md's inbox with a new kind (*Notifications*).

Added 2026-10-22.

## Words

- **Tournament** (بطولة): one game, a name, a **format**, a six-symbol
  **code**, an **organizer** (a player, or the platform for one made from the
  console: **official**), **entrants**, **matches**, and a **status**:
  `open` (taking entrants) → `running` → `done`, or `cancelled` from either
  of the first two.
- **Format**:
  - `knockout` (خروج المغلوب): duels in a bracket. Every match is two
    players; its winner goes on, its loser is out. A match is won by the
    first to win `need = best_of / 2 + 1` rounds (`best_of` 1, 3 or 5). A
    round where nobody is first alone (a draw) wins nobody anything: the
    pair plays another.
  - `league` (دوري): everybody at one table. One match, every entrant in
    it, `rounds` rounds (1 to 20). Each round gives every ranked entrant a
    point for every ranked entrant they beat (ranked worse than them); the
    most points after the last round wins.
- **Entrant** (مشارك): a player in the tournament. While it is open the
  entrants are in the order they came (their **seed** is that order); the
  start draws the seeds afresh, at random (القرعة). An entrant is `in`,
  `out` (lost a knockout match) or `withdrawn` (left, or was removed, once
  it ran).
- **Match** (مباراة): `trm_` and sixteen more. Knockout: a **stage** (1 is
  the first; the last, `stages`, is the final) and a **slot** in it (from
  0), two **sides**. League: one match, stage 1 slot 0, no sides (every
  entrant who has not withdrawn plays it). A match is `waiting` (a side is
  still to be decided), `ready` (it can be played) or `done` (decided).
- **Match room** (غرفة المباراة): a room made for a match (*Match rooms*).
  Its rounds are the match's rounds: each one that closes with results is
  **counted** (a **play**).
- **Bracket**: knockout's `P = 2^stages` slots, `P` the smallest power of
  two at or above the entrants. The seeds beyond the entrants are **byes**:
  a side nobody will ever take (**void**). Seeds sit in the standard order
  (seed 1 meets seed 2 in the final at the earliest; byes go to the top
  seeds).

## Storage (migration `00013_tournaments.sql`)

```sql
tournaments(
  id varchar(24) PK,              -- trn_ + 16
  code varchar(6) NOT NULL,       -- ROOM_CODE_ALPHABET, as a room's
  game_id varchar(64) NOT NULL REFERENCES games(id),
  organizer_id varchar(24) REFERENCES players(id),   -- NULL: official
  admin_id varchar(24) REFERENCES admins(id),        -- who made an official one
  name text NOT NULL,             -- 1 to 40 characters
  format varchar(8) NOT NULL,     -- knockout | league
  visibility varchar(8) NOT NULL, -- private | friends | public (official only)
  status varchar(10) NOT NULL,    -- open | running | done | cancelled
  best_of smallint NOT NULL,      -- knockout: 1 | 3 | 5; league: 1
  rounds smallint NOT NULL,       -- league: 1..20; knockout: 0
  size smallint NOT NULL,         -- the most entrants it takes
  config jsonb NOT NULL,          -- the game's room block when it was made
  stages smallint NOT NULL DEFAULT 0,  -- knockout, from the draw; league 1
  champion_id varchar(24) REFERENCES players(id),
  end_reason varchar(12),         -- below
  starts_at timestamptz,          -- the clock starts it then (or cancels it)
  created_at timestamptz NOT NULL, started_at, ended_at timestamptz
)
UNIQUE INDEX tournaments_code_live ON tournaments(code) WHERE status IN ('open','running')
INDEX tournaments_public ON tournaments(created_at DESC, id) WHERE visibility = 'public'
INDEX tournaments_game ON tournaments(game_id, created_at DESC)
INDEX tournaments_due ON tournaments(starts_at) WHERE status = 'open'

tournament_entrants(
  tournament_id varchar(24) REFERENCES tournaments(id) ON DELETE CASCADE,
  player_id varchar(24) REFERENCES players(id),
  seed integer NOT NULL,
  joined_at timestamptz NOT NULL,
  left_at timestamptz,            -- withdrew or removed, once it ran (or removed while open)
  removed boolean NOT NULL DEFAULT false,
  place integer,                  -- the final place, once known
  PRIMARY KEY (tournament_id, player_id)
)
INDEX tournament_entrants_player ON tournament_entrants(player_id)

tournament_matches(
  id varchar(24) PK,              -- trm_ + 16
  tournament_id varchar(24) NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
  stage integer NOT NULL, slot integer NOT NULL,
  player_a, player_b varchar(24) REFERENCES players(id),  -- NULL: not decided yet, or void
  void_a, void_b boolean NOT NULL DEFAULT false,
  status varchar(8) NOT NULL,     -- waiting | ready | done
  winner_id varchar(24) REFERENCES players(id),
  decided_by varchar(8),          -- play | bye | walkover | award | standings
  ready_at, decided_at timestamptz,
  UNIQUE (tournament_id, stage, slot)
)

tournament_plays(                 -- one counted round
  match_id varchar(24) REFERENCES tournament_matches(id) ON DELETE CASCADE,
  room_id varchar(24) NOT NULL,   -- no foreign key: the room row goes a day after its end
  round integer NOT NULL,
  results jsonb NOT NULL,         -- [{player, score, rank}], the match's players only
  winner_id varchar(24),          -- the round's one first place; NULL for a draw
  played_at timestamptz NOT NULL,
  PRIMARY KEY (match_id, room_id, round)
)

ALTER TABLE rooms ADD COLUMN match_id varchar(24) REFERENCES tournament_matches(id) ON DELETE SET NULL
UNIQUE INDEX rooms_match_open ON rooms(match_id) WHERE match_id IS NOT NULL AND status <> 'ended'
ALTER TABLE rooms ALTER COLUMN end_reason TYPE varchar(12)   -- "tournament" is ten letters
```

A code is drawn from the rooms' alphabet, uniformly, and inserted with `ON
CONFLICT (code) WHERE status IN ('open','running') DO NOTHING`; a collision
draws again. A tournament's code is free again once it is done or cancelled.
Every change to a tournament holds its row (`SELECT … FOR UPDATE`); the
standings and every tally are read from the plays, never kept twice.

`end_reason`: done `played` (the final was decided, or the league's last
round counted), `early` (the organizer or an admin finished a league before
its last round), `withdrawals` (a league left with fewer than two entrants);
cancelled `organizer`, `admin`, `too_few` (its `starts_at` came with fewer
entrants than it needs), `expired` (open for `TOURNAMENT_OPEN_FOR` with no
`starts_at`).

## The rules

### Making one

`POST /v1/tournaments {game, name, format, best_of?, rounds?, size, visibility?}`.

- The game must be published (`404 not_found`) and seat two at a table:
  its room block's `max` at least 2 (`game: not_multiplayer`).
- `name`: 1 to 40 characters once trimmed (`name: required`,
  `name: too_long`).
- `format`: `knockout` or `league` (`format: unknown`). Knockout needs the
  game to seat a duel: `min` at most 2 (`format: needs_duels`).
- Knockout: `best_of` 1, 3 or 5, default 1 (`best_of: 1_3_or_5`); `size`
  2 to `TOURNAMENT_MAX_ENTRANTS` (64), default 8 (`size: out_of_range`);
  `rounds` is 0 whatever was sent.
- League: `rounds` 1 to 20, default 5 (`rounds: 1_to_20`); `size` from
  `max(2, min)` to the game's `max` (capped at `RELAY_MAX_MEMBERS`), the
  game's table by default (`size: out_of_range`); `best_of` is 1 whatever
  was sent.
- `visibility`: `private` (the default: anybody with the code) or
  `friends` (the organizer's friends alone, no block either way). `public`
  is the console's (`visibility: not_allowed` from a player).
- Every problem comes back at once in one `422 invalid_fields`.
- The organizer is the first entrant (seed 1). They may withdraw while it
  is open and stay its organizer.
- The game's room block is copied into `config`: a manifest that changes
  later does not reshape a tournament already made.

### Entering and leaving

- **Join by code**, `POST /v1/tournaments/join {code}`: the code is read as
  a room's (case, spaces and dashes do not count; `code:
  must_be_6_symbols`). No open or running tournament holds it: `404
  tournament_not_found`, or `409 tournament_closed` when a finished one
  did. An entrant already in it gets it back (200, idempotent). Then, in
  this order: `403 removed` (the organizer removed them, for good); `403
  friends_only` (a friends tournament, and not the organizer's friend, or a
  block either way; an official one has no friends door); `409
  tournament_started` (running); `409 tournament_full` (`size` reached).
  The joiner's seed is the next number.
- **Leave**, `POST /v1/tournaments/{t}/leave`: while open the row is
  deleted (they may come back with the code); once running it is a
  **withdrawal**: `left_at` is set and every match they still have is lost
  (*Deciding*). `403 not_entrant` for anybody who is not a current entrant,
  `409 tournament_closed` once done or cancelled.
- **Remove**, `POST /v1/tournaments/{t}/remove {player}` (the organizer's):
  as a leave of theirs, and `removed` set so they never come back. `422
  invalid_fields` `player: not_an_entrant` / `is_you`.
- **Invite**, `POST /v1/tournaments/{t}/invites {players}` (any current
  entrant or the organizer, while open): as a room's invite (1 to 20, each
  the caller's friend with no block, not an entrant yet: `players.N:
  not_friends` / `already_entrant`, every problem at once, nobody invited
  unless all pass). Each gets a `tournament` notification keyed
  `tournament_invite:{t}` whose link is `/t/{code}`. `409 tournament_started`
  once it runs.

### Starting: the draw

`POST /v1/tournaments/{t}/start`, the organizer's (an official one: an
admin's, or the clock's at `starts_at`), from `open` only (`409
tournament_started` once running, `tournament_closed` after). It needs at
least two entrants, and for a league at least the game's `min`
(`409 too_few_entrants`).

- The seeds are drawn afresh, uniformly (`crypto/rand`), 1 to N.
- **Knockout**: `stages = log2(P)`; every match of every stage is written
  now, `P − 1` of them. Stage 1 slot `k` holds the seeds the standard order
  puts there (`P = 8`: 1–8, 4–5, 2–7, 3–6); a seed above N is a void side.
  Each first-stage match is then **settled** (*Deciding*): a seed facing a
  bye is through at once (`decided_by: bye`), and the matches with two
  players are `ready`.
- **League**: one match, `ready`, `stages = 1`.
- It writes `status: running`, `started_at`, and raises the notifications
  (*Notifications*).

### Playing a match: match rooms

`POST /v1/tournaments/{t}/matches/{m}/play` answers a **Room** (the rooms'
own view, `tournament` filled): the match's open room, joined (the rooms'
join, with all its refusals), or, when it has none, a new one made with the
caller as its host. Refusals first, in this order: `404 not_found` (no such
tournament for the caller, no such match in it); `409 tournament_not_running`
(open) / `tournament_closed`; `403 not_in_match` (knockout: not one of its
two sides, or withdrawn; league: not a current entrant); `409 match_waiting`
/ `match_decided`; then the rooms' (`in_another_room` names the room the
player is still in, `room_full`, `room_started`…).

A match room is a room like any other (docs/ROOMS.md) but for these:

- `rooms.match_id` names the match; one open room per match at most (made
  under a per-match advisory lock; a second `play` joins the first's).
- Its shape is the game's block, with the table set by the match: knockout
  `min` and `max` 2 (both players are needed to start), league `min`
  `max(2, game min)` and `max` the entrants not withdrawn. Its visibility is
  `private`; its door is the match.
- **The door**: only the match's players come in, by the code or by `play`
  (`403 not_in_match`, right after `kicked` in the join's order, in place of
  the friends door).
- **No kicks, no invites**: `room-kick` and `room-invite` are refused `409
  tournament_room`. Handing over the host, leaving, starting, finishing and
  ending are as in any room.
- **Counting**: whenever a round of a match room closes with results — the
  host's finish, an end during a round, the clock's finish of a round past
  its limit, the last member leaving during a round, the console's close
  during a round — the round's results, kept to the match's players, are
  counted in the same transaction (a play, once per `(match, room,
  round)`). A round that closes with none of the match's players ranked
  counts nothing. The game's server rewriting a round after it closed does
  not count it again.
  - Knockout: the round's winner is the one side ranked strictly better
    than the other (a side not ranked is behind one that is); equal ranks
    are a draw. A side that reaches `need` wins takes the match (`decided_by:
    play`).
  - League: every ranked entrant gets a point per ranked entrant ranked
    worse. The match is decided when `rounds` plays are counted.
- **A decided match closes its room**: when a count decides the match, the
  round closes as an **end** with reason `tournament` instead of a finish
  (`ended{reason: "tournament", by, round, results}`; the event `ended`,
  detail `{reason, round, match}`), and every socket closes with 4004. A
  match decided any other way (an award, a walkover, a finish, a
  cancellation) ends its open room the same way right after, with the
  latest round's results. `tournament` joins docs/ROOMS.md's end reasons.
- A room that ends without deciding its match (its host closed it, it
  idled, everybody left) leaves the match as it was: `play` makes a new
  room, and the tally is the match's, not the room's.
- The Room gains `tournament` (*Match on the Room*).

### Deciding

Every match decision holds the tournament's row. **Settling** a knockout
match whose sides are both known: two players still in → `ready`; one → it
is theirs (`bye` when the other side is void, `walkover` when the other
withdrew); none → decided with no winner, and the next match's side is void.
A match with a side still to come waits (`waiting`).

**A decided knockout match** sends its winner (or a void, for no winner) to
`(stage + 1, slot / 2)`, side `a` for an even slot and `b` for an odd one,
and settles that match. Its loser is `out`, placed `2^(stages − stage) + 1`
(the final's loser is 2nd, the semi-finals' 3rd, the quarter-finals' 5th).
The final decided, the tournament is `done` (`played`): the champion is
placed 1st.

**A league** is done (`played`) when its last round is counted, (`early`)
when the organizer or an admin finishes it — `POST /v1/tournaments/{t}/finish`,
at least one round counted (`409 nothing_played`), knockout refused `409
not_league` — or (`withdrawals`) when fewer than two entrants are left. The
places are the standings: points, then first places, then seed; withdrawn
entrants after everyone who stayed. The champion is the first.

**Awarding**, `POST /v1/tournaments/{t}/matches/{m}/award {player}` (the
organizer's, or an admin's): a `ready` knockout match is decided for one of
its two players (`decided_by: award`). The way to unstick a pair that never
plays. Refusals in this order: the organizer's (below); `409
tournament_not_running` / `tournament_closed`; `409 not_knockout`; `404
not_found` (no such match in it); `409 match_waiting` / `match_decided`;
`422 player: not_in_match`.

**Withdrawing** once it runs: knockout — the entrant's undecided match is
settled again (their side counts as gone: the opponent is through on a
walkover, at once when known, else when they arrive); league — they stop
playing and keep their points.

**Cancelling**, `POST /v1/tournaments/{t}/cancel` (the organizer's, or an
admin's): from `open` or `running`, for good (`cancelled`, reason
`organizer` / `admin`); no champion, no places. Every open match room ends
(`tournament`).

The organizer-only calls refuse in this order: `404 not_found` (cannot read
it), `403 organizer_only`, then `409 tournament_closed` and their own. An
admin is the organizer of every tournament. A removal also refuses `422
player: is_you` (an organizer removing themselves; they leave instead) and
`player: not_an_entrant`.

**Removed while it was open**: the rows are kept, marked, so the code never
lets them back while it is open; the draw deletes them (nobody joins a
running tournament). Once it runs, a removed entrant is a withdrawn one.

### The clock

The api's clock (`SWEEP_INTERVAL`, job `tournaments`, logging `started=N
cancelled=N`): an open tournament
whose `starts_at` has come is started, or cancelled `too_few` when it cannot
be; an open one with no `starts_at` older than `TOURNAMENT_OPEN_FOR` (7
days) is cancelled `expired`. Each re-checks its tournament under its row
lock.

## Reading them

Who may read a tournament: its organizer and its entrants (current or
former), anybody for a `public` one, the organizer's friends for a `friends`
one; everybody else is told `404 not_found` by id. Anybody holding the code
reads it by code.

| Operation | Method and path | Answer | Refusals |
|---|---|---|---|
| `tournament-create` | `POST /v1/tournaments` | 201 Tournament | 404 `not_found`, 422 `invalid_fields` |
| `tournaments` | `GET /v1/tournaments?view=&state=&game=&cursor=&limit=` | 200 `{items: [TournamentCard], next_cursor}` | 422 `invalid_fields` (`view: unknown`, `state: unknown`, `limit: 1_to_50`, `cursor: invalid`) |
| `tournament` | `GET /v1/tournaments/{t}` | 200 Tournament | 404 `not_found` |
| `tournament-by-code` | `GET /v1/tournaments/code/{code}` | 200 Tournament | 422 (`code: must_be_6_symbols`), 404 `tournament_not_found`, 409 `tournament_closed` |
| `tournament-join` | `POST /v1/tournaments/join` `{code}` | 200 Tournament | above |
| `tournament-leave` | `POST /v1/tournaments/{t}/leave` | 200 Tournament | 404, 403 `not_entrant`, 409 `tournament_closed` |
| `tournament-remove` | `POST /v1/tournaments/{t}/remove` `{player}` | 200 Tournament | 404, 403 `organizer_only`, 409, 422 |
| `tournament-invite` | `POST /v1/tournaments/{t}/invites` `{players}` | 200 `{invited}` | 404, 403 `not_entrant`, 409 `tournament_started` / `tournament_closed`, 422 |
| `tournament-start` | `POST /v1/tournaments/{t}/start` | 200 Tournament | 404, 403 `organizer_only`, 409 `tournament_started` / `tournament_closed` / `too_few_entrants` |
| `tournament-cancel` | `POST /v1/tournaments/{t}/cancel` | 200 Tournament | 404, 403 `organizer_only`, 409 `tournament_closed` |
| `tournament-finish` | `POST /v1/tournaments/{t}/finish` | 200 Tournament | 404, 403 `organizer_only`, 409 `tournament_not_running` / `tournament_closed` / `not_league` / `nothing_played` |
| `tournament-match-play` | `POST /v1/tournaments/{t}/matches/{m}/play` | 200 Room | above (*Playing a match*) |
| `tournament-match-award` | `POST /v1/tournaments/{t}/matches/{m}/award` `{player}` | 200 Tournament | 404, 403 `organizer_only`, 409 `not_knockout` / `match_waiting` / `match_decided` / `tournament_not_running` / `tournament_closed`, 422 `player: not_in_match` |

`view`: `mine` (the default: the caller is an entrant, current or former,
or the organizer) or `public` (official ones, open or running, everybody's).
`state`: `active` (the default: open and running) or `past` (done and
cancelled). `game` narrows to one game. Newest first (`created_at`
descending, then id); `limit` 20 by default, 1 to 50; `next_cursor` opaque
and null on the last page.

### Tournament

```json
{
  "id": "trn_7f3k9d2m8q1x4b6n",
  "code": "T4K9QX",
  "link": "http://localhost:8080/t/T4K9QX",
  "name": "بطولة الجمعة",
  "game": { "id": "fixture.xo", "name": "إكس أو", "icon": "https://…", "orientation": "portrait" },
  "format": "knockout",
  "visibility": "private",
  "status": "running",
  "official": false,
  "organizer": Card | null,
  "best_of": 3, "need": 2, "rounds": 0, "size": 8, "min": 2,
  "stages": 3, "stage": 2,
  "entrants": [Entrant],
  "matches": [Match],
  "standings": [Standing],
  "champion": Card | null,
  "you": { "entrant": true, "organizer": false, "status": "in", "match": "trm_…" } | null,
  "end_reason": null,
  "starts_at": null, "created_at": "…", "started_at": "…", "ended_at": null
}
```

- `Card` is the players' card (`{id, nickname, handle, avatar, avatar_url}`).
- `need` is the wins a knockout match takes (1 for a league). `min` the
  entrants it needs to start. `stage` is knockout's earliest stage with an
  undecided match (null when none, and for a league).
- `entrants`, by seed: `Card` fields plus `seed`, `status` (`in`, `out`,
  `withdrawn`) and `place` (null until known). Removed players are not
  listed while it is open.
- `matches`: by stage, then slot. A `Match` is `{id, stage, slot, status,
  sides: [Side, Side] ([] for a league), winner (id or null), decided_by,
  played (rounds counted), room ({id, code, status, round} of its open
  room, or null), ready_at, decided_at}`; a `Side` is `{player: Card |
  null, wins, void, withdrawn}`.
- `standings`: a league's table at any time — `{rank, player: Card, points,
  wins (first places), played (rounds ranked in), withdrawn}`, ranked as the
  places are — and, for a knockout, the entrants with a place, by place,
  once it has any (ranks may be shared there: 3, 3).
- `you`: null for a caller who is neither an entrant nor the organizer.
  `match` is the caller's match that is `ready` or `waiting` (knockout), or
  the league's while they are in it; null otherwise.

### TournamentCard

The lists' row: `{id, code, name, game, format, visibility, status,
official, organizer, entrants (a count), size, best_of, rounds, stage,
stages, played (a league's rounds counted), you, your_match, champion,
starts_at, created_at, started_at, ended_at}`. `your_match` is `{id, status, stage, opponent: Card | null,
room: {id, code, status, round} | null}` for the caller's current match,
else null.

## Match on the Room

docs/ROOMS.md's Room gains `tournament`, null for any other room:

```json
"tournament": {
  "id": "trn_…", "name": "بطولة الجمعة", "format": "knockout",
  "match": "trm_…", "stage": 2, "stages": 3,
  "best_of": 3, "need": 2, "rounds": 0, "played": 1,
  "tally": [{ "player": "ply_…", "wins": 1, "points": 0 }]
}
```

`tally` is the match's: the two sides (knockout) with their wins, or the
league's table with points and first places, best first. `played` the
rounds counted so far. It is read when the Room is, so every route's answer
and every `hello` carry the tally as it stands.

The bridge's context (`GET /v1/bridge/context`) gains the same block in its
`room` (`tournament`, null outside a match room), names left out.

## Notifications

A new kind, `tournament`, raised by the platform (Arabic, as every platform
notification), each keyed so a repeat updates its row:

| Key | To | Title | Body | Link · data |
|---|---|---|---|---|
| `tournament_invite:{t}` | each invitee | دعوة لبطولة | `{inviter} يدعوك لبطولة {name} ({game})` | `/t/{code}` · `{tournament, code, game}` |
| `tournament_started:{t}` | every entrant | بدأت البطولة | `{name}: القرعة صارت، شوف مباراتك` (league: `{name}: الدوري بدأ`) | `/t/{code}` · `{tournament, code, game}` |
| `tournament_match:{m}` | both players of a match that turns `ready` (not the league's) | مباراتك جاهزة | `ضد {opponent} · {stage words} · {name}` | `/t/{code}` · `{tournament, code, game, match}` |
| `tournament_done:{t}` | every entrant | انتهت البطولة | `{champion} بطل {name}` (the champion: `مبروك! صرت بطل {name}`) | `/t/{code}` · `{tournament, code, game}` |
| `tournament_cancelled:{t}` | every entrant | ألغيت البطولة | `{name} ما راح تكمل` | `/t/{code}` · `{tournament, code, game}` |

Stage words: the final النهائي, the semi-finals نصف النهائي, the
quarter-finals ربع النهائي, otherwise `الدور {stage}`. A push carries
`tournament` and `match` among its data (docs/PUSH.md, "The message").

## The link

`GET /t/{code}` (operation `tournament-link`, public, docs/JOINING.md's
page in a tournament's words): the tournament's name, its game and the code
while it is open or running; the not-found form otherwise. The phones claim
`/t/*` with `/r/*` (the Android filters and the iOS components). The app
catches it and opens the tournament by its code.

## The console

Admin routes (scope `admin`; changes take the role `admin`, written to the
audit log, area `tournaments`, actions `tournament.created`,
`tournament.started`, `tournament.cancelled`, `tournament.finished`,
`match.awarded`):

| Operation | Method and path |
|---|---|
| `admin-tournaments` | `GET /v1/admin/tournaments?status=&game=&cursor=&limit=` (`status`: `live` the default, `open`, `running`, `done`, `cancelled`, `all`) → `{items: [TournamentCard], next_cursor}` |
| `admin-tournament` | `GET /v1/admin/tournaments/{t}` → Tournament (`you` null) |
| `admin-tournament-create` | `POST /v1/admin/tournaments {game, name, format, best_of?, rounds?, size, starts_at?}` → 201 Tournament: official and `public`, no organizer, no entrant |
| `admin-tournament-start` | `POST /v1/admin/tournaments/{t}/start` |
| `admin-tournament-cancel` | `POST /v1/admin/tournaments/{t}/cancel` |
| `admin-tournament-finish` | `POST /v1/admin/tournaments/{t}/finish` |
| `admin-tournament-award` | `POST /v1/admin/tournaments/{t}/matches/{m}/award {player}` |

An admin may act on any tournament, a player's too. `starts_at` must be in
the future (`starts_at: in_the_past`).

## The app

- **Ways in.** The Play tab's "البطولات" (its own section under the room:
  the player's tournaments that need them first — a match ready — and the
  key to all of them), a multiplayer game's store page ("سوّي بطولة",
  and its public tournaments), the Me tab's "بطولاتي", the inbox and pushes
  (`tournament`: the tournament's page), the link `/t/{code}`, and from
  inside a game (*The bridge*).
- **The tournaments page**: "بطولاتك" (active), "بطولات رسمية" (public),
  "السابقة" (past, paged); "سوّي بطولة" and "انضم ببطولة" (the code).
- **The tournament page**: open — the code (copy, link, QR), the entrants,
  "ادعُ", join or leave, and the organizer's "ابدأ البطولة"; running —
  "مباراتك" (the caller's match, its tally, "العب" → the match room's
  lobby), the bracket by stage (a stage at a time, chosen on a chip strip:
  nothing scrolls sideways) or the league's table; done — the champion and
  the places. The organizer's menu: award, finish (league), cancel, remove.
- **The match room**: the lobby says which match it is (stage, the two
  sides and their wins, "شوف البطولة"), and offers no invite, QR or kick.
  After a round the results say the tally. A room ended `tournament` keeps
  its last results with "رجوع للبطولة" in place of the way home, and the
  hub does not take the player back for it (it is no notice).

### The bridge (`window.gathering.tournament`)

- `tournament.current()` → in a match room, `{id, name, format, match,
  stage, stages, bestOf, need, rounds, played, tally: [{player, nickname,
  wins, points}]}` (the Room's block, camel-cased, each tally line named);
  null elsewhere.
- `room.current()` gains `tournament` (the same block, or null).
- `tournament.open()` → the tournament's page over the frame (in a match
  room); refused `not_in_tournament` elsewhere.
- `tournament.create({format?})` → on a page outside a room: the app's own
  sheet to make a tournament for the page's game (the format chosen when
  given), answering `{id, code}` once made, after which the tournament's
  page takes the page's place. Refused `in_room`, `not_a_room_game`,
  `cancelled` (the sheet closed), or the platform's codes.
- `tournament.list()` → the caller's active tournaments of this game and its
  public ones: `[{id, code, name, format, status, entrants, size, official,
  yourMatchReady}]`.
- `tournament.join({code?})` → as `room.join`: with a code, joined for this
  game only (another game's is `another_game`); without, the app's join
  sheet. Answers `{id, code}`; the tournament's page then takes the page's
  place.
- The frame's menu gains "سوّي بطولة" (outside a room, a game that seats
  two) and "البطولة" (in a match room).
- The sample games (`fixture.xo`, `fixture.tap` in `../gathering_games`)
  show the match on their screen in a match room and a "بطولة" key on
  their first screen.

## Not yet

Double elimination, a third-place match, groups before a knockout,
spectators of a match, prizes, a tournament's own chat, rematches of a
decided match, and seeding by skill.
