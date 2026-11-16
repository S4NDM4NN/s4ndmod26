-- Mail that could not be delivered, as reported by Resend's webhook.  A permanent bounce or a spam complaint marks the address
-- at once; temporary failures are counted and the count starts over after a delivery.  Changing the address clears all of it.
ALTER TABLE accounts
    ADD COLUMN mail_bounced_at   timestamptz,
    ADD COLUMN mail_bounce_why   text NOT NULL DEFAULT '',
    ADD COLUMN mail_soft_bounces int  NOT NULL DEFAULT 0;

-- Events already handled (the webhook is delivered at least once, so a retry must not count twice).
CREATE TABLE mail_events (
    event_id    text        PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT now()
);
