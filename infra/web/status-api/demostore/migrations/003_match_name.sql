-- A name for a match ("QuakeCon 2003 final, game 2"); empty means unnamed.
-- It does not change the demos, so it does not bump the match's version.
ALTER TABLE demo_matches ADD COLUMN name text NOT NULL DEFAULT '';
