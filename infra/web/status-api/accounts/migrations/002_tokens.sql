-- One-time tokens sent by email: confirming an address, resetting a password.  Only a hash is kept.
CREATE TABLE account_tokens (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    purpose    text        NOT NULL,            -- verify | reset
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX account_tokens_account ON account_tokens (account_id, purpose);

-- An old demo-upload key (its label) that an account has redeemed.  One key, one account.
CREATE TABLE account_demo_keys (
    label       text        PRIMARY KEY,
    account_id  bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_demo_keys_account ON account_demo_keys (account_id);
