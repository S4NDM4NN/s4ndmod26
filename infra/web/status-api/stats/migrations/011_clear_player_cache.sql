-- The cached summary gained "aliases"; the cache is only a copy, so clear it and let it refill.  (Do the same whenever the summary's shape changes.)
DELETE FROM player_cache;
