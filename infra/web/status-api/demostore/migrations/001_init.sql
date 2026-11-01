-- A match is one game that one or more players' demos are of.  Demos are kept
-- by content (sha256), so the same file uploaded twice is one row; who
-- uploaded it is a separate claim, so each uploader can remove only their own.
CREATE TABLE demo_matches (
    id          text PRIMARY KEY,
    map         text NOT NULL,
    -- bumped whenever the match's demos change; the merged output is built
    -- for one version
    version     int NOT NULL DEFAULT 1,
    merged_key  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE demo_files (
    id              bigserial PRIMARY KEY,
    sha256          text NOT NULL UNIQUE,
    match_id        text NOT NULL REFERENCES demo_matches(id) ON DELETE CASCADE,
    protocol        int NOT NULL,
    size            bigint NOT NULL,
    map             text NOT NULL,
    server_id       text NOT NULL DEFAULT '',
    recorder        text NOT NULL DEFAULT '',
    recorder_slot   int NOT NULL DEFAULT -1,
    spectator       boolean NOT NULL DEFAULT false,
    start_ms        int NOT NULL,
    end_ms          int NOT NULL,
    frames          int NOT NULL,
    -- puts this demo's clock on the match's: match time = own + base_offset_ms
    base_offset_ms  int NOT NULL DEFAULT 0,
    gametype        int NOT NULL DEFAULT 0,
    rounds          int NOT NULL DEFAULT 0,
    players         jsonb NOT NULL DEFAULT '[]',
    warning         text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX demo_files_match ON demo_files (match_id);
CREATE INDEX demo_files_map ON demo_files (map);

CREATE TABLE demo_uploads (
    file_id      bigint NOT NULL REFERENCES demo_files(id) ON DELETE CASCADE,
    owner        text NOT NULL,      -- the label of the uploader key
    filename     text NOT NULL,      -- as it was named; for display only
    uploaded_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (file_id, owner)
);
CREATE INDEX demo_uploads_owner ON demo_uploads (owner, uploaded_at DESC);
