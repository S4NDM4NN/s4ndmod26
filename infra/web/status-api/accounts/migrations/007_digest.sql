-- The weekly summary email (see the digest package).  Opt-in: the signup form offers it, ticked; accounts that already exist are not opted in.
-- digest_token is what the unsubscribe link in each mail carries (random, one per account, kept when the owner turns the mail off so old
-- links keep working); digest_last_sent_at drives the schedule (at least 7 days between mails).
ALTER TABLE accounts
    ADD COLUMN digest_opt_in      boolean     NOT NULL DEFAULT false,
    ADD COLUMN digest_opted_in_at timestamptz,
    ADD COLUMN digest_token       text,
    ADD COLUMN digest_last_sent_at timestamptz;
CREATE UNIQUE INDEX accounts_digest_token ON accounts (digest_token) WHERE digest_token IS NOT NULL;
