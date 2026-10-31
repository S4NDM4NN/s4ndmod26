-- A series is matches that belong together: three stopwatch games of a final,
-- a best-of-five.  A key links matches it has uploaded demos of; the key that
-- made the series (owner) names, orders and dissolves it.
CREATE TABLE demo_series (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    owner       text NOT NULL,       -- the label of the key that made it
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE demo_matches
    ADD COLUMN series_id  text REFERENCES demo_series(id) ON DELETE SET NULL,
    ADD COLUMN series_pos int NOT NULL DEFAULT 0;
CREATE INDEX demo_matches_series ON demo_matches (series_id, series_pos);
