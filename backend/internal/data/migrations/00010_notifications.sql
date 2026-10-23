-- +goose Up

-- The inbox: one feed per player, newest first, each row read or unread.
-- A player raises one by asking, accepting or inviting; a game's server
-- raises one through the bridge; the console (M8) will raise system ones.
-- docs/FRIENDS.md is the contract.
CREATE TABLE notifications (
    id         varchar(24) PRIMARY KEY,                     -- ntf_ + 16
    player_id  varchar(24) NOT NULL REFERENCES players (id),   -- the recipient
    kind       text        NOT NULL,                        -- enum.NoticeKinds
    actor_id   varchar(24) REFERENCES players (id),         -- who caused it, when a player did
    game_id    varchar(64) REFERENCES games (id),           -- a game's, or the room's game for an invite
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    deep_link  text        NOT NULL DEFAULT '',
    data       jsonb       NOT NULL DEFAULT '{}',           -- {room, code, game} for an invite, {player} for a request
    -- Idempotency, per recipient and game: a raise with a key already there
    -- updates that row in place and brings it back to the top, unread.
    key        text,
    -- When it was raised, or raised again: the feed's order, and what the
    -- housekeeping job's 90 days are counted from.
    created_at timestamptz NOT NULL,
    read_at    timestamptz
);

-- One row per recipient, game and key. A platform notification has no game,
-- and NULL never equals NULL, so the game is read as '' here.
CREATE UNIQUE INDEX notifications_key ON notifications (player_id, COALESCE(game_id, ''), key) WHERE key IS NOT NULL;
-- The feed, a page at a time by (created_at, id).
CREATE INDEX notifications_feed ON notifications (player_id, created_at DESC, id);
-- The badge: how many are unread.
CREATE INDEX notifications_unread ON notifications (player_id) WHERE read_at IS NULL;

-- +goose Down
DROP TABLE notifications;
