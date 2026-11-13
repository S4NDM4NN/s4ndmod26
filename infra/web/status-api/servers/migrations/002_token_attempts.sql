-- What the last refused claim with a token found, so the host can see it on their account page.
ALTER TABLE server_tokens ADD COLUMN last_attempt timestamptz;
ALTER TABLE server_tokens ADD COLUMN last_error text NOT NULL DEFAULT '';
