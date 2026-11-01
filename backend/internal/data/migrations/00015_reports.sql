-- +goose Up

-- A player reporting another (docs/REPORTS.md). One open report per reporter
-- and player: reporting again replaces it. An admin resolves it.
CREATE TABLE reports (
    id          varchar(24) PRIMARY KEY,                     -- rpt_ + 16
    reporter_id varchar(24) NOT NULL REFERENCES players (id),
    player_id   varchar(24) NOT NULL REFERENCES players (id), -- who it names
    reason      varchar(12) NOT NULL,                        -- enum.ReportReasons
    note        varchar(300) NOT NULL DEFAULT '',
    status      varchar(8)  NOT NULL DEFAULT 'open',         -- enum.ReportStatuses
    created_at  timestamptz NOT NULL,
    resolved_at timestamptz,
    resolved_by varchar(24) REFERENCES admins (id),
    CHECK (reporter_id <> player_id)
);

CREATE UNIQUE INDEX reports_one_open ON reports (reporter_id, player_id) WHERE status = 'open';
-- The console's list, newest first, and how many name a player.
CREATE INDEX reports_list ON reports (created_at DESC, id);
CREATE INDEX reports_against ON reports (player_id) WHERE status = 'open';

-- +goose Down
DROP TABLE reports;
