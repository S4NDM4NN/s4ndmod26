-- When to look at an opted-in account again after finding it had not played (so the weekly job does not recount it every few minutes).
ALTER TABLE accounts ADD COLUMN digest_next_check_at timestamptz;
