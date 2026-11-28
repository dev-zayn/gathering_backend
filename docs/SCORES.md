# Scores: a game played alone

The contract for a score from a game played alone: a reaction time, a run
against the clock, a hand won against the computer. A room's round has its
results (docs/ROOMS.md, "Results"), and the friends' leaderboard reads them
(docs/FRIENDS.md); a game played outside a room had nowhere to put a score
but the phone's own storage, which sign-out wipes. Now a game declares
**boards** in its manifest, and each board keeps every player's best.

The platform (`backend/internal/biz/scores`, the player routes, the manifest's
check) and the app (`gathering_app`, the bridge's `scores.*`) both build to
it; when they disagree this file decides, and when it is wrong it is fixed
here first.

A board is apart from the room's results: a reaction time in milliseconds
and a round's points never share a column, and nothing here changes a
leaderboard, a history or a tournament.

## The manifest's `scores` block

```json
"scores": {
  "boards": [
    { "id": "reaction", "order": "low", "min": 80, "max": 2000 },
    { "id": "sprint", "order": "low" }
  ]
}
```

- `id`: 2 to 32 of `a-z`, `0-9` and `-`, starting with a letter; unique in
  the block. It is the board's name on the wire and in the table, so it
  never changes once players have scores on it.
- `order`: `high` (the highest score is the best: points) or `low` (the
  lowest is: a time). A board's order never changes; a game that wants the
  other order makes a new board.
- `min`, `max`: optional bounds, numbers, `min` at most `max`. A score
  outside them is refused. They are the game's own sanity line (no human
  reacts in 20 ms), not a defence against a page that lies: see "Trust".
- At most 8 boards. A manifest without the block, or with no boards, takes
  no scores.

`Manifest.Check` (the registry, a publisher's build and the docs' MCP
`check_manifest`) refuses a bad block under `scores.boards`, the first
problem only: `too_many` (more than 8), `bad id: X`, `duplicate id: X`,
`unknown order: X` (`order` missing or not `high`/`low`), `min above max: X`.

The platform reads the boards from the published game's manifest, kept
whole in its row: no column of their own, no migration for a new board.

## The table (migration `00018_scores.sql`)

```
best_scores
  game_id     varchar(64)  references games (id)
  board       varchar(32)
  player_id   varchar(24)  references players (id)
  best        numeric      the player's best on this board
  plays       integer      scores sent on this board, the best included
  best_at     timestamptz  when the best was sent
  last_at     timestamptz  the latest score's
  primary key (game_id, board, player_id)
```

One row per player and board; a score that is not a new best only counts a
play. Every score is not kept, only the best.

## `POST /v1/games/{game}/scores` (operation `game-score`, scope `session`)

`{ "board": "reaction", "score": 231 }`. **200**:

`{ board, score, best, new_best, plays }`

`best` is the player's best after this score, `new_best` whether this score
became it (strictly better than the last best, or the board's first score),
`plays` the count after this one.

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 404 | `not_found` | no game by that id in the store (published), the test builds included |
| 422 | `invalid_fields` | `board: required`, `board: unknown` (not one of the game's boards), `score: required`, `score: below_min`, `score: above_max` |
| 429 | `too_many_requests` | more than 200 scores an hour from one player in one game (`retry_after`) |

The game is the path's, so the app sends the frame's own game: a page never
names another game's board.

## `GET /v1/games/{game}/scores/{board}` (operation `game-scores`, scope `session`)

`?scope=friends`, the only one, also the default (`scope: friends_only`).
**200**:

`{ game: {id, name, icon}, board, order, entries: [{ player: Card, rank,
best, plays, best_at, you }] }`

The caller and their friends who have a score on the board (the caller only
with one of their own). Ranked by `best` in the board's order; equal bests
share a rank (1, 1, 3) and are listed by `best_at` (who got there first),
then nickname. A player switched off is left out. `404 not_found` for a
game not in the store or a board it does not declare.

## Trust

A score is the page's word, as a host's end is a round's (docs/ROOMS.md):
the app sends what the page says, under the player's own session, and a
player with their own session could send any number within the bounds.
The bounds and the hourly limit keep honest pages honest and a careless one
from flooding; a board among friends is worth what friends make of it. A
game whose scores must be proven would need its server to write them, as a
room's results can (`results:write`); v1 has no such route for scores.

## The bridge (the app, `gathering_app/docs/GAME_BRIDGE.md`, "Scores")

| Function | Returns | Notes |
| --- | --- | --- |
| `scores.submit({ board, score })` | `{ board, score, best, newBest, plays }` | `POST …/scores` for the frame's game. Rejects `invalid_fields` (also, before anything is sent, a missing board or a score that is not a finite number), `not_found`, `too_many_requests`, or the kind of failure (`offline`, `timeout`, `failed`). |
| `scores.board(board)` | `{ board, order, entries: [{ id, nickname, avatarUrl, rank, best, plays, you }] }` | The friends' board, never a copy kept on the phone. Rejects `not_found`, `offline`, `timeout`, `failed`. |

Inside a room or outside one alike; nothing about a room is read. A game
checks `gathering.scores` before using it: an app from before scores has no
such namespace, and a page opened in a browser has no `gathering` at all.
Either way it keeps its own best on the phone and says nothing.

## Deleting an account

`DELETE /v1/me` (docs/ACCOUNTS.md) deletes the player's rows here. A switched
off player's rows stay and are left out of every board.

## Not in v1

A game's server writing scores, boards beyond friends (everybody, the
week's), a board per day, and the app's own page for a board: the game
shows its board itself, through `scores.board`.
