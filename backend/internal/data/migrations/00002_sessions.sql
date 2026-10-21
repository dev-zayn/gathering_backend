-- +goose Up

-- One signed-in phone. It lasts as long as its refresh token: every refresh
-- moves last_seen_at, signing out sets revoked_at, and a player going means
-- their sessions go.
CREATE TABLE sessions (
    id           varchar(24)  PRIMARY KEY,              -- ses_ + 16
    player_id    varchar(24)  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    device       varchar(120) NOT NULL DEFAULT '',      -- what the app said about itself
    created_at   timestamptz  NOT NULL DEFAULT now(),
    last_seen_at timestamptz  NOT NULL DEFAULT now(),
    expires_at   timestamptz  NOT NULL,
    revoked_at   timestamptz
);

CREATE INDEX sessions_player_idx ON sessions (player_id);

-- Single-use, so they need a history rather than a column: a spent token coming
-- back means someone has a copy, and the session dies. Stored hashed.
CREATE TABLE refresh_tokens (
    hash       char(64)    PRIMARY KEY,              -- sha256, hex
    session_id varchar(24) NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz
);

CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);

-- A phone a session can push to: its Firebase token. One per session, and a
-- token on one session at a time, since it names the app install, not the
-- person. Gone with its session; pushes skip a revoked or expired one, and
-- the sweep removes it.
CREATE TABLE devices (
    id           varchar(24) PRIMARY KEY,                -- dev_ + 16
    session_id   varchar(24) NOT NULL UNIQUE REFERENCES sessions (id) ON DELETE CASCADE,
    player_id    varchar(24) NOT NULL,
    platform     varchar(8)  NOT NULL,                   -- enum.Platforms
    fcm_token    text        NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

-- A push finds the phones of one player.
CREATE INDEX devices_player_idx ON devices (player_id);

-- +goose Down
DROP TABLE devices;
DROP TABLE refresh_tokens;
DROP TABLE sessions;
