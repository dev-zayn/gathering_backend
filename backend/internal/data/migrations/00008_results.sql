-- +goose Up

-- A room's results, kept for good: one row per ranked player. They come from
-- the host's end, or from the game's own server, whose word is the stronger:
-- once it has written a room's results, the host's end no longer touches
-- them. A room's rows are always one write's: one submitted_by, one
-- finished_at, one players.
--
-- The room row is deleted a day after the end, its members with it (the
-- sweeper), and the results outlive both. So room_id has no foreign key, and
-- what the reads still need from the room is copied in: its code, and the
-- seat each player held, which orders a tie.
CREATE TABLE results (
    room_id      varchar(24) NOT NULL,                          -- room_ + 16; no foreign key
    player_id    varchar(24) NOT NULL REFERENCES players (id),
    game_id      varchar(64) NOT NULL REFERENCES games (id),
    code         varchar(6)  NOT NULL,                          -- the room's code, copied
    seat         integer     NOT NULL,                          -- the player's seat at the write
    score        numeric     NOT NULL,
    rank         integer     NOT NULL CHECK (rank >= 1),        -- 1 is the best; ties share one
    players      integer     NOT NULL,                          -- how many were ranked in this room
    finished_at  timestamptz NOT NULL,
    submitted_by text        NOT NULL,                          -- enum.Submitters: host | game
    PRIMARY KEY (room_id, player_id)
);

-- A player's history, newest first, paged by (finished_at, room_id).
CREATE INDEX results_history ON results (player_id, finished_at DESC, room_id);
-- A player's results in one game, newest first: the leaderboards (M6).
CREATE INDEX results_game ON results (game_id, player_id, finished_at DESC);

-- +goose Down
DROP TABLE results;
