-- +goose Up

-- Announcements: one message from the platform to many players at once, sent
-- from the console. Each is written with one notification per recipient
-- (kind system, key announcement:<id>) in the same transaction, so a row here
-- always has its notifications and its reach is what they number.
-- docs/CONSOLE.md, "Announcements", is the contract.
CREATE TABLE announcements (
    id         varchar(24) PRIMARY KEY,                      -- ann_ + 16
    admin_id   varchar(24) NOT NULL REFERENCES admins (id),  -- who sent it
    audience   jsonb       NOT NULL,                         -- as sent: {kind, players?, game?}
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    game_id    varchar(64) REFERENCES games (id),            -- the game the row opens, if any
    reach      integer     NOT NULL,                         -- the notifications written
    created_at timestamptz NOT NULL
);

-- The console's list, newest first, a page at a time by (created_at, id).
CREATE INDEX announcements_recent ON announcements (created_at DESC, id);

-- +goose Down
DROP TABLE announcements;
