-- +goose Up

-- A room: one game, one host, a code to join by. Its shape is the game's
-- manifest room block as it stood when the room was made (config), so a
-- manifest that changes later does not reshape a room already open. The
-- relay keeps the live side in Redis (the frame counter, the history, the
-- shared state); this row is the membership's anchor and what survives.
-- What a room was, and why it ended, is written to logs.room_events (00004).
CREATE TABLE rooms (
    id         varchar(24) PRIMARY KEY,                     -- room_ + 16
    -- Six symbols of ROOM_CODE_ALPHABET, upper case. Unique among the rooms
    -- still open (rooms_code_open below), so an ended room frees its code.
    code       varchar(6)  NOT NULL,
    game_id    varchar(64) NOT NULL REFERENCES games (id),
    host_id    varchar(24) NOT NULL REFERENCES players (id),
    status     varchar(8)  NOT NULL DEFAULT 'lobby',        -- enum.RoomStatuses
    visibility varchar(8)  NOT NULL DEFAULT 'private',      -- enum.Visibilities
    -- Asked for a Wi-Fi relay. The phones discover it (M7); the platform
    -- only remembers that it was asked.
    lan        boolean     NOT NULL DEFAULT false,
    config     jsonb       NOT NULL,                        -- model.RoomBlock

    -- The shared state lives in Redis while the room is open and is copied
    -- here when it ends: the last data and its version.
    state         jsonb,
    state_version integer NOT NULL DEFAULT 0,

    end_reason varchar(8),                                  -- enum.EndReasons
    -- The scores the end carried, ranks filled in. The results table and the
    -- history arrive with M5 and are written from the same end.
    results    jsonb,

    created_at timestamptz NOT NULL DEFAULT now(),
    -- Moves on create, join, start, end, a socket connecting, and at most once
    -- a minute on the relay's pings. The sweeper ends a lobby nobody has
    -- touched for ROOM_LOBBY_IDLE.
    active_at  timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at   timestamptz
);

-- The code is what a player types, so only one open room may hold it at once.
-- Making a room inserts with ON CONFLICT on this index and draws again.
CREATE UNIQUE INDEX rooms_code_open ON rooms (code) WHERE status <> 'ended';
-- The sweeper's two questions: lobbies idle since, and rooms playing since.
CREATE INDEX rooms_status_active ON rooms (status, active_at);
CREATE INDEX rooms_game_idx ON rooms (game_id);

-- Who is in a room. A row outlives a leave (left_at) and a kick (kicked, for
-- good), so a rejoin is told apart from a stranger and a kicked player stays
-- out. A member is current while left_at is null.
CREATE TABLE room_members (
    room_id   varchar(24) NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    player_id varchar(24) NOT NULL REFERENCES players (id),
    role      varchar(16) NOT NULL,                         -- enum.RoomRoles
    seat      integer     NOT NULL,                         -- 0-based; the host takes 0
    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at   timestamptz,
    kicked    boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (room_id, player_id)
);

-- A seat holds one current member; a leave frees it for the next joiner.
CREATE UNIQUE INDEX room_members_seat ON room_members (room_id, seat) WHERE left_at IS NULL;
-- "Which open room is this player in": one per player, checked on every
-- create and join.
CREATE INDEX room_members_player ON room_members (player_id) WHERE left_at IS NULL;

-- +goose Down
DROP TABLE room_members;
DROP TABLE rooms;
