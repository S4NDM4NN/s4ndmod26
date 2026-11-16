-- When an account that was never confirmed (and has a player key linked) was warned that it will be deleted.  One warning each.
ALTER TABLE accounts ADD COLUMN unconfirmed_warned_at timestamptz;
