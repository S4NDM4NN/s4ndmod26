-- The mod pk3 the server says it runs (its name carries the build version), so the play page can tell whether a
-- browser can join it with the site's own copy.
ALTER TABLE servers ADD COLUMN pk3 text NOT NULL DEFAULT '';
