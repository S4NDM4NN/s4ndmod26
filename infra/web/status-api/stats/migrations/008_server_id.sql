-- Stats can come from any registered server's agent, not only the local folder.  server_id is the registry's id of the server a game
-- came from ('' = the local folder), so everything a server sent can be deleted in one go (PurgeServer).  A pulled game's match is
-- "<server_id>-<its name on that server>" so names from different servers never collide; src_match is the name on the server.
ALTER TABLE games ADD COLUMN server_id text NOT NULL DEFAULT '';
ALTER TABLE games ADD COLUMN src_match text NOT NULL DEFAULT '';
UPDATE games SET src_match = match;
CREATE INDEX games_server ON games (server_id);

ALTER TABLE ingest_files ADD COLUMN server_id text NOT NULL DEFAULT '';
ALTER TABLE ingest_files DROP CONSTRAINT ingest_files_pkey;
ALTER TABLE ingest_files ADD PRIMARY KEY (server_id, name);
