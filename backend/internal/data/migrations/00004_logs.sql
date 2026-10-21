-- +goose Up

-- Every log the platform writes: what it did, next to what it is. Their own
-- schema, so the tables that grow without limit are easy to tell apart.
CREATE SCHEMA logs;

-- The audit trail: who decided what. One table with an area column, because the
-- console's only question is what happened. Nothing expires it: an audit trail
-- is evidence, and dropping it is a policy decision, not a schema one.
CREATE TABLE logs.audit (
    id      varchar(24) PRIMARY KEY,           -- aud_ + 16
    at      timestamptz NOT NULL,
    area    varchar(16) NOT NULL,              -- auth, players, games, rooms, admin
    actor   varchar(64) NOT NULL,              -- an id, or a phone before there is one
    action  varchar(40) NOT NULL,
    subject varchar(64) NOT NULL,
    detail  jsonb       NOT NULL DEFAULT '{}'
);

-- Read newest first: across every area, one area, or one thing's whole history.
CREATE INDEX audit_at_idx ON logs.audit (at DESC);
CREATE INDEX audit_subject_idx ON logs.audit (area, subject, at DESC);
CREATE INDEX audit_actor_idx ON logs.audit (actor);
CREATE INDEX audit_action_idx ON logs.audit (action);

-- What happened in a room: joins, leaves, starts, ends, kicks, and why the
-- sweeper ended one. This is the table that grows: one row per event.
CREATE TABLE logs.room_events (
    id        varchar(24) PRIMARY KEY,         -- rev_ + 16
    at        timestamptz NOT NULL,
    room_id   varchar(24) NOT NULL,
    game_id   varchar(64) NOT NULL,
    player_id varchar(24) NOT NULL DEFAULT '', -- '' when the clock or the host's game did it
    event     varchar(24) NOT NULL,
    detail    jsonb       NOT NULL DEFAULT '{}'
);

-- A room's history in order, and the bare time index for the ops view and
-- whatever retention sweep comes later.
CREATE INDEX room_events_room_idx ON logs.room_events (room_id, at);
CREATE INDEX room_events_at_idx ON logs.room_events (at);

-- +goose Down
DROP SCHEMA logs CASCADE;
