-- Addresses that Resend told us it cannot deliver to (a permanent bounce, or the person marked a mail as spam), kept for 30 days
-- even when no account has the address any more, so a bot cannot register it again and again.
CREATE TABLE mail_suppressed (
    email_key text        PRIMARY KEY,
    reason    text        NOT NULL,
    at        timestamptz NOT NULL DEFAULT now()
);
