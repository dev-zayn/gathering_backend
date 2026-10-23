-- +goose Up

-- Tournaments: one game, a format (a knockout bracket of duels, or a league
-- at one table), a code to join by, its entrants and its matches. A match is
-- played in rooms (rooms.match_id below); every round of such a room that
-- closes with results is counted as a play, and the tallies, the standings
-- and the places are read from the plays. docs/TOURNAMENTS.md is the
-- contract.
CREATE TABLE tournaments (
    id           varchar(24) PRIMARY KEY,                    -- trn_ + 16
    -- Six symbols of ROOM_CODE_ALPHABET, upper case, unique among the
    -- tournaments still open or running (tournaments_code_live below).
    code         varchar(6)  NOT NULL,
    game_id      varchar(64) NOT NULL REFERENCES games (id),
    -- The player who made it; NULL for an official one, made from the
    -- console by admin_id.
    organizer_id varchar(24) REFERENCES players (id),
    admin_id     varchar(24) REFERENCES admins (id),
    name         text        NOT NULL,
    format       varchar(8)  NOT NULL,                       -- enum.TournamentFormats
    visibility   varchar(8)  NOT NULL DEFAULT 'private',     -- enum.TournamentVisibilities
    status       varchar(10) NOT NULL DEFAULT 'open',        -- enum.TournamentStatuses
    best_of      smallint    NOT NULL DEFAULT 1,             -- knockout: 1, 3 or 5
    rounds       smallint    NOT NULL DEFAULT 0,             -- league: 1 to 20
    size         smallint    NOT NULL,                       -- the most entrants it takes
    config       jsonb       NOT NULL,                       -- model.RoomBlock, the game's when it was made
    stages       smallint    NOT NULL DEFAULT 0,             -- set by the draw
    champion_id  varchar(24) REFERENCES players (id),
    end_reason   varchar(12),                                -- enum.TournamentEnds
    -- When the clock starts it (or cancels it, short of entrants). Optional.
    starts_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    ended_at     timestamptz
);

-- The code is what a player types, so only one live tournament holds it.
CREATE UNIQUE INDEX tournaments_code_live ON tournaments (code) WHERE status IN ('open', 'running');
-- The app's list of official tournaments, newest first.
CREATE INDEX tournaments_public ON tournaments (created_at DESC, id) WHERE visibility = 'public';
CREATE INDEX tournaments_game ON tournaments (game_id, created_at DESC);
-- The clock's question: which open tournaments are due.
CREATE INDEX tournaments_due ON tournaments (starts_at) WHERE status = 'open';
CREATE INDEX tournaments_organizer ON tournaments (organizer_id) WHERE organizer_id IS NOT NULL;

-- Who is in a tournament. While it is open, leaving deletes the row; once
-- it runs, a withdrawal keeps it (left_at), so the bracket still names them.
-- A removed player keeps a row so the code never lets them back.
CREATE TABLE tournament_entrants (
    tournament_id varchar(24) NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    player_id     varchar(24) NOT NULL REFERENCES players (id),
    seed          integer     NOT NULL,                      -- join order; the draw's once it runs
    joined_at     timestamptz NOT NULL,
    left_at       timestamptz,
    removed       boolean     NOT NULL DEFAULT false,
    place         integer,                                   -- the final place, once known
    PRIMARY KEY (tournament_id, player_id)
);

CREATE INDEX tournament_entrants_player ON tournament_entrants (player_id);

-- The matches. A knockout writes the whole bracket at the draw, the later
-- stages with their sides to come; a league writes one match. A side is a
-- player, or void (nobody will ever take it: a bye, or a match nobody won),
-- or still to come (NULL and not void).
CREATE TABLE tournament_matches (
    id            varchar(24) PRIMARY KEY,                   -- trm_ + 16
    tournament_id varchar(24) NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    stage         integer     NOT NULL,                      -- 1 is the first; the last is the final
    slot          integer     NOT NULL,                      -- from 0, within the stage
    player_a      varchar(24) REFERENCES players (id),
    player_b      varchar(24) REFERENCES players (id),
    void_a        boolean     NOT NULL DEFAULT false,
    void_b        boolean     NOT NULL DEFAULT false,
    status        varchar(8)  NOT NULL DEFAULT 'waiting',    -- enum.MatchStatuses
    winner_id     varchar(24) REFERENCES players (id),
    decided_by    varchar(10),                               -- enum.MatchDecisions
    ready_at      timestamptz,
    decided_at    timestamptz,
    UNIQUE (tournament_id, stage, slot)
);

-- One counted round of a match: the results it closed with, kept to the
-- match's players, and its one winner (NULL for a draw). Kept with the
-- tournament for good; the room it was played in goes a day after its end.
CREATE TABLE tournament_plays (
    match_id  varchar(24) NOT NULL REFERENCES tournament_matches (id) ON DELETE CASCADE,
    room_id   varchar(24) NOT NULL,
    round     integer     NOT NULL,
    results   jsonb       NOT NULL,
    winner_id varchar(24),
    played_at timestamptz NOT NULL,
    PRIMARY KEY (match_id, room_id, round)
);

-- A room made for a match. At most one open room per match: a second player
-- asking to play joins the first one's.
ALTER TABLE rooms ADD COLUMN match_id varchar(24) REFERENCES tournament_matches (id) ON DELETE SET NULL;
CREATE UNIQUE INDEX rooms_match_open ON rooms (match_id) WHERE match_id IS NOT NULL AND status <> 'ended';
-- A match room whose match was decided ends with the reason tournament,
-- which is longer than every reason before it.
ALTER TABLE rooms ALTER COLUMN end_reason TYPE varchar(12);

-- +goose Down
ALTER TABLE rooms ALTER COLUMN end_reason TYPE varchar(8);
DROP INDEX rooms_match_open;
ALTER TABLE rooms DROP COLUMN match_id;
DROP TABLE tournament_plays;
DROP TABLE tournament_matches;
DROP TABLE tournament_entrants;
DROP TABLE tournaments;
