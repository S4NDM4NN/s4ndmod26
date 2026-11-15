-- An index of the replays other servers have: what the games list shows (map, length, play of the game) and where to fetch the replay from.
-- The replay files themselves are never stored here: each server keeps and serves its own (its agent, or a public bucket), and the viewer
-- fetches them from base_url.  Everything in this table is deleted with the server's other data (PurgeServer).
CREATE TABLE replay_index (
    server_id  text        NOT NULL,
    name       text        NOT NULL,             -- the replay's name on that server
    match      text        NOT NULL UNIQUE,      -- how the site names it (<server_id>-<name>, as in games.match)
    summary    jsonb       NOT NULL,             -- the server's <name>.summary.json
    sum_size   bigint      NOT NULL,             -- size of that file when read: a different size means read it again
    has_rpl    boolean     NOT NULL DEFAULT false, -- the raw replay (with its .txt) is still there, so the 3D viewer can load it
    base_url   text        NOT NULL,             -- https://host[:port]/v1/replays: where its files are
    started_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, name)
);
CREATE INDEX replay_index_started ON replay_index (started_at DESC NULLS LAST);
