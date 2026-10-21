-- +goose Up

-- The console's list of names. Admins sign in through an OIDC provider, which
-- holds who they are, their roles and their MFA; nothing here decides who gets
-- in. A row is written the first time somebody opens the console, and its id
-- is ours: the audit log names adm_…, never the provider's id. Their actions
-- go to the audit log, which arrives with its own migration.
CREATE TABLE admins (
    id          varchar(24)  PRIMARY KEY,          -- adm_ + 16, ours
    sso_subject varchar(255) NOT NULL UNIQUE,      -- the token's sub; dev:<name> on a laptop
    name        varchar(120) NOT NULL DEFAULT '',  -- as the provider last gave it
    email       varchar(254) NOT NULL DEFAULT '',
    created_at  timestamptz  NOT NULL DEFAULT now(),
    seen_at     timestamptz                        -- the last sign-in, once there has been one
);

-- +goose Down
DROP TABLE admins;
