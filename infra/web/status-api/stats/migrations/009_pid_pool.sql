-- Who played (the player's key, from the game's identity check; '' for bots and players without one) and which pool a game counts in.
-- A pool keeps stats apart: 'official' (our standard-config servers), 'community' (anyone's), and later competitive ones per team size.
-- The pool is stamped when the game is first stored, so changing a server's flag later does not rewrite history.
ALTER TABLE game_players ADD COLUMN pid text NOT NULL DEFAULT '';
CREATE INDEX game_players_pid ON game_players (pid, game_id) WHERE pid <> '';
ALTER TABLE games ADD COLUMN pool text NOT NULL DEFAULT 'official';
CREATE INDEX games_pool ON games (pool);
-- games stored from another server before this existed (none in production yet) were community games
UPDATE games SET pool = 'community' WHERE server_id <> '';
