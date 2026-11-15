-- Per-game stats written by the game server (<g_statsPath>/<match>.jsonl, one line per player per round-end / disconnect /
-- map-end) and ingested by the status API.  A game is one match; players are not tracked across games (yet).

CREATE TABLE games (
    id                bigserial PRIMARY KEY,
    match             text NOT NULL UNIQUE,            -- the replay's name, e.g. replays_20261017_012254
    map               text NOT NULL DEFAULT '',
    started_at        timestamptz,                     -- from the match name (the server's clock, taken as UTC)
    first_ingested_at timestamptz NOT NULL DEFAULT now()
);

-- One row per stats line: a player's numbers for one round, or up to the moment they left.  A stopwatch game has two
-- rounds; the page sums them.
CREATE TABLE game_players (
    id              bigserial PRIMARY KEY,
    game_id         bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    src_offset      bigint NOT NULL,                   -- byte offset of the line in its file: ingesting a file twice adds nothing
    event           text NOT NULL,                     -- round_end | disconnect | map_end
    round           int  NOT NULL,
    slot            int  NOT NULL,
    name            text NOT NULL,
    bot             boolean NOT NULL,
    team            text NOT NULL,                     -- axis | allies
    class           int  NOT NULL,
    kills           int  NOT NULL DEFAULT 0,
    deaths          int  NOT NULL DEFAULT 0,
    suicides        int  NOT NULL DEFAULT 0,
    teamkills       int  NOT NULL DEFAULT 0,
    gibs            int  NOT NULL DEFAULT 0,
    revives         int  NOT NULL DEFAULT 0,
    damage_given    int  NOT NULL DEFAULT 0,
    damage_received int  NOT NULL DEFAULT 0,
    team_damage     int  NOT NULL DEFAULT 0,
    score           int  NOT NULL DEFAULT 0,
    UNIQUE (game_id, src_offset)
);
CREATE INDEX game_players_game ON game_players (game_id, round);

-- OSP's five numbers per weapon.  Packs and the syringe are rows too (packs: hits = used, atts = dropped; syringe: hits =
-- revives) and World holds deaths with no weapon behind them.
CREATE TABLE game_player_weapons (
    player_id  bigint NOT NULL REFERENCES game_players(id) ON DELETE CASCADE,
    weapon     text   NOT NULL,
    hits       int    NOT NULL DEFAULT 0,
    atts       int    NOT NULL DEFAULT 0,
    kills      int    NOT NULL DEFAULT 0,
    deaths     int    NOT NULL DEFAULT 0,
    headshots  int    NOT NULL DEFAULT 0,
    PRIMARY KEY (player_id, weapon)
);

-- How far into each stats file has been ingested (the files only ever grow).
CREATE TABLE ingest_files (
    name       text PRIMARY KEY,
    byte_pos   bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
