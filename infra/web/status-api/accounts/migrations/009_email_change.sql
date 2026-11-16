-- A new email address waiting to be confirmed.  The account keeps its old address until the link in the mail sent to
-- the new one is used.  One pending change per account; only a hash of the token is kept.
CREATE TABLE account_email_changes (
    account_id bigint      PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    email      text        NOT NULL,
    email_key  text        NOT NULL,
    token_hash bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX account_email_changes_token ON account_email_changes (token_hash);
