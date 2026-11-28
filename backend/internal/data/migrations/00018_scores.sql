-- +goose Up

-- Scores from a game played alone (docs/SCORES.md): one row per player and
-- board, the best kept and the plays counted. The boards themselves are the
-- game's manifest's (its scores block, kept whole in games.manifest), so a
-- new board needs no migration.
CREATE TABLE best_scores (
    game_id   varchar(64) NOT NULL REFERENCES games (id),
    board     varchar(32) NOT NULL,                          -- the manifest's board id
    player_id varchar(24) NOT NULL REFERENCES players (id),
    best      numeric     NOT NULL,
    plays     integer     NOT NULL CHECK (plays >= 1),
    best_at   timestamptz NOT NULL,
    last_at   timestamptz NOT NULL,
    PRIMARY KEY (game_id, board, player_id)
);

-- Deleting an account deletes a player's rows.
CREATE INDEX best_scores_player ON best_scores (player_id);

-- +goose Down
DROP TABLE best_scores;
