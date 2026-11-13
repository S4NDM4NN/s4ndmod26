-- An admin can suspend an account: no login, no permissions, the record stays.
ALTER TABLE accounts ADD COLUMN suspended_at timestamptz;
ALTER TABLE accounts ADD COLUMN suspended_reason text NOT NULL DEFAULT '';

-- What admins did.  No foreign keys: the log must outlive the accounts it names, so names are copied in.
CREATE TABLE account_audit (
    id          bigserial   PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor_id    bigint,
    actor_name  text        NOT NULL DEFAULT '',
    action      text        NOT NULL,        -- grant, revoke, suspend, unsuspend, delete
    target_id   bigint,
    target_name text        NOT NULL DEFAULT '',
    detail      text        NOT NULL DEFAULT ''
);
CREATE INDEX account_audit_at ON account_audit (at DESC, id DESC);
