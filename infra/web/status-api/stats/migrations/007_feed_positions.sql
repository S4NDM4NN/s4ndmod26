-- Where each feed event happened (when the replay says), so the replay viewer can place it on the map, plus the new support events.
ALTER TABLE game_events ADD COLUMN pos_x int, ADD COLUMN pos_y int, ADD COLUMN pos_z int;

-- Read every analysis again: the feed is rebuilt from it with positions and the new event types (medpack, ammo, strike, tapout).
DELETE FROM ingest_files WHERE name LIKE '%.analysis';
