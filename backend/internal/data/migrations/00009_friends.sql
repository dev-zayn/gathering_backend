-- +goose Up

-- Friends: one row per pair, the lower id first, so a pair has one row
-- whoever asked. A row is a request until the other player accepts it; a
-- decline, a cancel, a removal and a block delete it. Where each player
-- stands with the other is read from this row and from blocks below.
-- docs/FRIENDS.md is the contract.
CREATE TABLE friendships (
    player_lo    varchar(24) NOT NULL REFERENCES players (id),
    player_hi    varchar(24) NOT NULL REFERENCES players (id),
    status       text        NOT NULL,                     -- enum.FriendshipStatuses: requested | accepted
    requested_by varchar(24) NOT NULL,                     -- one of the two
    created_at   timestamptz NOT NULL,                     -- the request
    responded_at timestamptz,                              -- the accept
    PRIMARY KEY (player_lo, player_hi),
    CHECK (player_lo < player_hi)
);

-- The primary key finds a pair, and every row of a player who is the lower
-- of the two; this finds the rows where they are the higher.
CREATE INDEX friendships_hi_idx ON friendships (player_hi);

-- A block is one player refusing another, in one direction. It ends any
-- friendship or request between the two and keeps them from making a new
-- one, inviting each other, or meeting in a friends-only room.
CREATE TABLE blocks (
    blocker_id varchar(24) NOT NULL REFERENCES players (id),
    blocked_id varchar(24) NOT NULL REFERENCES players (id),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);

-- "Who blocked me": the other direction of the primary key.
CREATE INDEX blocks_blocked_idx ON blocks (blocked_id);

-- +goose Down
DROP TABLE blocks;
DROP TABLE friendships;
