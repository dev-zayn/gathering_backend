-- +goose Up

-- Rounds: a room plays again and again without a new room. A game's end
-- finishes a round and puts the room back in its lobby with the same code,
-- members and sockets; only closing it, the last member leaving or idling
-- ends the room. docs/ROOMS.md, "Rounds", is the contract.

-- How many rounds have started: 0 in a fresh lobby; while playing, the round
-- being played; back in the lobby, the round just finished. It only grows.
ALTER TABLE rooms ADD COLUMN round integer NOT NULL DEFAULT 0;
-- A room from before rounds played one at most. One that ended in its lobby
-- with scores (M5 allowed that) has rows too: a room's round is never below
-- a round it has rows for.
UPDATE rooms SET round = 1 WHERE started_at IS NOT NULL OR id IN (SELECT room_id FROM results);

-- Results are per round. Every row so far is its room's one round.
ALTER TABLE results ADD COLUMN round integer NOT NULL DEFAULT 1;
ALTER TABLE results DROP CONSTRAINT results_pkey;
ALTER TABLE results ADD PRIMARY KEY (room_id, round, player_id);

-- A player's history, newest first, one item per round, paged by
-- (finished_at, room_id, round).
DROP INDEX results_history;
CREATE INDEX results_history ON results (player_id, finished_at DESC, room_id, round);

-- +goose Down
DROP INDEX results_history;
-- The old key holds one round a room: the latest stays.
DELETE FROM results r USING (SELECT room_id, max(round) AS last FROM results GROUP BY room_id) l
WHERE r.room_id = l.room_id AND r.round < l.last;
ALTER TABLE results DROP CONSTRAINT results_pkey;
ALTER TABLE results ADD PRIMARY KEY (room_id, player_id);
ALTER TABLE results DROP COLUMN round;
CREATE INDEX results_history ON results (player_id, finished_at DESC, room_id);
ALTER TABLE rooms DROP COLUMN round;
