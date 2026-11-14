-- Signing in a program (the launcher) through the website: the program asks for a login, the person approves it on
-- the site while signed in there, and the program is handed a short session that may only link keys.
ALTER TABLE account_sessions ADD COLUMN scope text NOT NULL DEFAULT '';   -- '' = everything, 'link' = /api/link/* only

CREATE TABLE device_logins (
    device_hash bytea       PRIMARY KEY,            -- sha256 of the secret the program polls with
    user_code   text        NOT NULL UNIQUE,        -- what the person compares and types, "ABCD-EFGH"
    label       text        NOT NULL,               -- what the program called itself, shown on the approval page
    ip          text        NOT NULL,
    account_id  bigint      REFERENCES accounts(id) ON DELETE CASCADE,   -- set when approved
    denied      boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
