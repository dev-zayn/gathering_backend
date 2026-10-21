-- +goose Up

-- The one-time code: sixty seconds, one use, and the only credential that ever
-- touches the frame. Stored hashed, like a refresh token.
CREATE TABLE oauth_codes (
    hash      char(64)    PRIMARY KEY,             -- sha256, hex
    game_id   varchar(64) NOT NULL,
    player_id varchar(24) NOT NULL,

    -- The room the game was opened into, when it was. The token minted from
    -- the code carries it, so a game's server knows which room it is in.
    room_id varchar(24) NOT NULL DEFAULT '',

    -- What was decided at mint time: the game's grant, cut to what a person
    -- present may hold. No game is trusted to enforce it.
    scopes jsonb NOT NULL DEFAULT '[]',

    -- Live says a person was present. An elevated scope exists only under this.
    live boolean NOT NULL DEFAULT false,

    -- PKCE, and where to send the answer. Both are fixed when the code is minted
    -- and must match when it is spent; both are empty for the in-app launch.
    challenge    varchar(128)  NOT NULL DEFAULT '',
    redirect_uri varchar(2048) NOT NULL DEFAULT '',

    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE INDEX oauth_codes_expiry_idx ON oauth_codes (expires_at);

-- The game's long-lived half. No revoked column and no expiry: the game's
-- place in the player's library is the lifetime, and every renewal asks
-- whether it is still there.
CREATE TABLE game_tokens (
    hash       char(64)    PRIMARY KEY,             -- sha256, hex
    game_id    varchar(64) NOT NULL,
    player_id  varchar(24) NOT NULL,
    scopes     jsonb       NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz
);

-- Removing a game from a library ends the tokens for that pair.
CREATE INDEX game_tokens_library_idx ON game_tokens (player_id, game_id);

-- +goose Down
DROP TABLE game_tokens;
DROP TABLE oauth_codes;
