-- A demo that spans several games (a spectator's, say) is kept as one row per
-- game, each in the match of that game and each looking at its own stretch of
-- the file (window, in the demo's own clock).  They share the stored file, so
-- the content hash is unique only together with the segment (0: the whole file).
ALTER TABLE demo_files DROP CONSTRAINT demo_files_sha256_key;
ALTER TABLE demo_files ADD COLUMN segment int NOT NULL DEFAULT 0;
ALTER TABLE demo_files ADD COLUMN window_from_ms int NOT NULL DEFAULT 0;
ALTER TABLE demo_files ADD COLUMN window_to_ms int NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX demo_files_sha_segment ON demo_files (sha256, segment);
