-- +goose Up

-- A person. The phone is unique across the platform, which is what makes a
-- number enough to sign in; it is nullable so a player who arrives through a
-- provider (the identities table) need not have one.
CREATE TABLE players (
    id          varchar(24)  PRIMARY KEY,              -- ply_ + 16
    phone       varchar(20)  UNIQUE,                   -- E.164, +9647XXXXXXXXX
    nickname    varchar(20)  NOT NULL,
    -- The @name friends search by: unique, lowercase, letters digits and _.
    handle      varchar(24)  NOT NULL UNIQUE,
    handle_changed_at timestamptz,
    avatar_key  varchar(255) NOT NULL DEFAULT '',     -- avatars/<id>.<ext>, or a bundled id
    bio         varchar(120) NOT NULL DEFAULT '',
    disabled    boolean      NOT NULL DEFAULT false,
    language    varchar(2)   NOT NULL DEFAULT 'ar',    -- enum.Languages
    last_seen_at timestamptz,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now()
);

-- A sign-in through a provider (google, apple): the seam SSO lands in. Empty
-- until a provider ships; the table is here so adding one is a handler, not a
-- migration.
CREATE TABLE identities (
    provider   varchar(16)  NOT NULL,
    subject    varchar(255) NOT NULL,
    player_id  varchar(24)  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    created_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX identities_player_idx ON identities (player_id);

-- +goose Down
DROP TABLE identities;
DROP TABLE players;
