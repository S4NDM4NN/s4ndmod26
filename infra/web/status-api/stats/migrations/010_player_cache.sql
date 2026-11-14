-- A copy of each player's totals so the player page does not add up every game on each view.  It is only a copy: anything that
-- adds, changes or deletes a player's games marks the row stale in the same transaction, and a read recomputes a stale or missing
-- row from the games.  Deleting this table's rows is always safe.
CREATE TABLE player_cache (
    pid         text PRIMARY KEY,
    summary     jsonb       NOT NULL,
    stale       boolean     NOT NULL DEFAULT false,
    computed_at timestamptz NOT NULL DEFAULT now()
);
