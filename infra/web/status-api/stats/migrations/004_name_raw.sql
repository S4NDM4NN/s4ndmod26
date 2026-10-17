-- The player's name with its colour codes, as the game wrote it (name_raw); `name` stays the plain text. Empty for lines written before
-- the game added the field.
ALTER TABLE game_players ADD COLUMN name_raw text NOT NULL DEFAULT '';
