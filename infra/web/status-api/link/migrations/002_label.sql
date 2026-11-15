-- A name for each linked key ("Chrome on Windows", the computer's name), private to the account's owner.
ALTER TABLE player_links ADD COLUMN label text NOT NULL DEFAULT '';
