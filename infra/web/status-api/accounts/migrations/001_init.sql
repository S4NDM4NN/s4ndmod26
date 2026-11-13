CREATE TABLE accounts (
    id               bigserial PRIMARY KEY,
    username         text        NOT NULL,                -- as typed, shown to people
    username_key     text        NOT NULL UNIQUE,         -- lower case, what is compared
    email            text        NOT NULL,
    email_key        text        NOT NULL UNIQUE,
    email_verified_at timestamptz,
    password_hash    text        NOT NULL,                -- argon2id, PHC string
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- What an account may do beyond playing: demo_upload, host_server, admin.  Deleted with the account.
CREATE TABLE account_permissions (
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    permission text        NOT NULL,
    granted_by bigint,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, permission)
);

-- Only a hash of the token is kept, so a copy of the database cannot be used to log in.
CREATE TABLE account_sessions (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX account_sessions_account ON account_sessions (account_id);
