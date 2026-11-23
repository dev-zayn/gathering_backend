-- +goose Up

-- The studios that make games, each one sign-in to the console
-- (docs/PUBLISHERS.md). An admin makes the row; the publisher chooses its
-- password through a one-time link, so no password ever passes through an
-- admin's hands.
CREATE TABLE publishers (
    id            varchar(24)  PRIMARY KEY,               -- pub_ + 16
    name          varchar(120) NOT NULL,                  -- the studio, as its games show it
    email         varchar(254) NOT NULL UNIQUE,           -- the sign-in, lower-cased
    password_hash varchar(72)  NOT NULL DEFAULT '',       -- bcrypt; empty until a link is used
    -- The one-time link: only the hash of its token is kept, as with a client
    -- secret. Empty when there is none waiting.
    setup_hash       varchar(64)  NOT NULL DEFAULT '',
    setup_expires_at timestamptz,
    disabled_at   timestamptz,
    created_by    varchar(24)  NOT NULL DEFAULT '',       -- the admin, adm_…
    created_at    timestamptz  NOT NULL DEFAULT now(),
    seen_at       timestamptz                             -- the last sign-in
);

CREATE INDEX publishers_setup ON publishers (setup_hash) WHERE setup_hash <> '';

-- Whose game it is. Empty for every game made before publishers, ours
-- included: those are run from the console's Games pages alone.
ALTER TABLE games ADD COLUMN publisher_id varchar(24) REFERENCES publishers (id);
CREATE INDEX games_publisher ON games (publisher_id) WHERE publisher_id IS NOT NULL;

-- A version of a game on its way to players: the manifest as read from the
-- address the publisher gave, and the store copy it will have once
-- published. The live game is still the games row; a build reaches it only
-- by being published.
CREATE TABLE game_builds (
    id      varchar(24) PRIMARY KEY,                      -- bld_ + 16
    game_id varchar(64) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    -- testing, in_review, approved, rejected (open); published, withdrawn.
    status  varchar(16) NOT NULL DEFAULT 'testing',

    -- The manifest's half, as the games row keeps it.
    name          varchar(120)  NOT NULL,
    version       varchar(32)   NOT NULL,
    manifest_url  varchar(2048) NOT NULL,
    entry         varchar(2048) NOT NULL,
    manifest      jsonb         NOT NULL DEFAULT '{}',
    fetched_at    timestamptz   NOT NULL,
    orientation   varchar(16)   NOT NULL DEFAULT 'portrait',
    splash        boolean       NOT NULL DEFAULT false,
    scopes        jsonb         NOT NULL DEFAULT '[]',
    permissions   jsonb         NOT NULL DEFAULT '[]',
    room          jsonb         NOT NULL DEFAULT '{}',
    redirect_uris jsonb         NOT NULL DEFAULT '[]',

    -- The store copy it will have. Pictures are keys in the bucket: its own
    -- under games/<id>/builds/<build>/, or the live game's it inherited.
    tagline     varchar(160) NOT NULL DEFAULT '',
    description text         NOT NULL DEFAULT '',
    category    varchar(32)  NOT NULL DEFAULT 'other',
    icon_key    varchar(255) NOT NULL DEFAULT '',
    screenshots jsonb        NOT NULL DEFAULT '[]',

    -- What the publisher tells testers and reviewers, and the reviewer's
    -- answer on the last decision.
    notes        varchar(2000) NOT NULL DEFAULT '',
    review_note  varchar(2000) NOT NULL DEFAULT '',
    reviewed_by  varchar(24)   NOT NULL DEFAULT '',       -- adm_…
    reviewed_at  timestamptz,

    submitted_at timestamptz,
    published_at timestamptz,
    closed_at    timestamptz,                             -- published or withdrawn
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- One open build per game: the rule, kept by the database.
CREATE UNIQUE INDEX game_builds_open ON game_builds (game_id)
    WHERE status IN ('testing', 'in_review', 'approved', 'rejected');
CREATE INDEX game_builds_game ON game_builds (game_id, created_at DESC);
-- The review queue, oldest submission first.
CREATE INDEX game_builds_review ON game_builds (submitted_at) WHERE status = 'in_review';

-- Who tests a game: kept per game, build after build. No foreign key on the
-- player, as with the library: a deleted account deletes its rows itself.
CREATE TABLE game_testers (
    game_id   varchar(64) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    player_id varchar(24) NOT NULL,
    code      varchar(16) NOT NULL DEFAULT '',            -- the invite that brought them
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, player_id)
);

CREATE INDEX game_testers_player ON game_testers (player_id, joined_at DESC);

-- The codes a publisher hands out. Typed in the app, they make a tester.
CREATE TABLE tester_invites (
    code       varchar(16) PRIMARY KEY,                   -- eight symbols of the rooms' alphabet
    game_id    varchar(64) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    label      varchar(60) NOT NULL DEFAULT '',
    max_uses   integer     NOT NULL DEFAULT 0,            -- 0: no limit
    uses       integer     NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_by varchar(24) NOT NULL DEFAULT '',           -- pub_… or adm_…
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tester_invites_game ON tester_invites (game_id, created_at DESC);

-- The build a room plays when a tester made it; NULL for the live game. No
-- foreign key: a room's history does not hang on a build.
ALTER TABLE rooms ADD COLUMN build_id varchar(24);

-- +goose Down
ALTER TABLE rooms DROP COLUMN build_id;
DROP TABLE tester_invites;
DROP TABLE game_testers;
DROP TABLE game_builds;
ALTER TABLE games DROP COLUMN publisher_id;
DROP TABLE publishers;
