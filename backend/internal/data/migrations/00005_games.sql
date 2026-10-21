-- +goose Up

-- The registry: one row per game. The text a player reads is one string in
-- one language: the console translates its own chrome, the data says one
-- thing. It starts empty — every game is a URL we fetch, ours included.
CREATE TABLE games (
    id          varchar(64)  PRIMARY KEY,            -- yourstudio.game
    name        varchar(120) NOT NULL,
    tagline     varchar(160) NOT NULL DEFAULT '',
    description text         NOT NULL DEFAULT '',
    publisher   varchar(120) NOT NULL DEFAULT '',
    icon_key    varchar(255) NOT NULL DEFAULT '',    -- games/<id>/icon.png in our bucket
    screenshots jsonb        NOT NULL DEFAULT '[]',
    -- enum.Categories, checked in code: adding one is a Go change, not a
    -- migration. The same goes for orientation, listing and tier below.
    category    varchar(32)  NOT NULL DEFAULT 'other',
    -- Featured games lead the store, whatever their tier. An admin's switch.
    featured    boolean      NOT NULL DEFAULT false,

    -- How the frame opens: the manifest's, not ours.
    orientation varchar(16)  NOT NULL DEFAULT 'portrait',
    splash      boolean      NOT NULL DEFAULT false,
    version     varchar(32)  NOT NULL,

    -- Only 'published' is visible to a player; the other two are 404, because
    -- their existence is not a player's business. A game starts unpublished.
    listing     varchar(16)  NOT NULL DEFAULT 'unpublished',

    first_party boolean      NOT NULL DEFAULT false,
    tier        varchar(16)  NOT NULL DEFAULT 'partner',

    -- The manifest as it arrived, whole: launching and the bridge each read
    -- different parts of it.
    manifest_url varchar(2048) NOT NULL DEFAULT '',
    entry        varchar(2048) NOT NULL DEFAULT '',
    manifest     jsonb         NOT NULL DEFAULT '{}',
    fetched_at   timestamptz,

    -- A re-read of a published game that asks for more — new scopes, other
    -- permissions, another room shape — is kept here, whole, until an admin
    -- reads it and accepts. Players carry on with the manifest above.
    -- held_at set is the whole of "something is waiting".
    held     jsonb,
    held_url varchar(2048) NOT NULL DEFAULT '',
    held_at  timestamptz,

    -- The scope keys the manifest asks for, checked against scopes below, and
    -- the device permissions the app asks the player for. An admin reads
    -- both before publishing; a player is never asked about the scopes.
    scopes      jsonb NOT NULL DEFAULT '[]',
    permissions jsonb NOT NULL DEFAULT '[]',

    -- The room block: players min/max, host, pace, lan, state, rejoin,
    -- spectators. One column, whole, so a room is made without parsing the
    -- whole document.
    room jsonb NOT NULL DEFAULT '{}',

    -- The game as an OAuth client. Registration issues a secret; only its hash
    -- is kept, so a leaked backup cannot be used to spend anybody's code.
    secret_hash varchar(64) NOT NULL DEFAULT '',    -- sha256, hex; empty until issued

    -- Where the game may be sent back to. Empty means any path on the entry's
    -- own origin, which still refuses somebody else's domain.
    redirect_uris jsonb NOT NULL DEFAULT '[]',

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX games_category ON games (category);
-- The store's own order, over the rows the store shows.
CREATE INDEX games_published ON games (featured DESC, first_party DESC, id) WHERE listing = 'published';

-- The scope vocabulary, one row per thing our API exposes to a game's server.
-- A manifest naming a key that is not here is refused at registration.
CREATE TABLE scopes (
    key      varchar(32) PRIMARY KEY,
    -- Elevated needs a person present and never survives a renewal; nothing
    -- is yet.
    elevated boolean     NOT NULL DEFAULT false
);

INSERT INTO scopes (key, elevated) VALUES
  ('identity:read', false),
  ('friends:read',  false),
  ('notify:send',   false),
  ('results:write', false);

-- Which games a player has: the ones they opened at least once. Nothing is
-- agreed to — the token a game gets carries whatever its manifest asks for
-- today. Removing the row is the revocation.
CREATE TABLE library (
    player_id      varchar(24) NOT NULL,
    game_id        varchar(64) NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    added_at       timestamptz NOT NULL DEFAULT now(),
    last_played_at timestamptz,
    plays          integer     NOT NULL DEFAULT 0,
    -- Leads with player_id, so it is also the index for "this player's games".
    PRIMARY KEY (player_id, game_id)
);

-- How far a game has got is counted the other way round.
CREATE INDEX library_game_idx ON library (game_id);

-- +goose Down
DROP TABLE library;
DROP TABLE scopes;
DROP TABLE games;
