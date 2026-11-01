-- +goose Up

-- A player who deleted their account (docs/ACCOUNTS.md): the row stays,
-- emptied, so the games they played still have somebody in that seat; this
-- says when. Null for everybody else.
ALTER TABLE players ADD COLUMN deleted_at timestamptz;

-- +goose Down
ALTER TABLE players DROP COLUMN deleted_at;
