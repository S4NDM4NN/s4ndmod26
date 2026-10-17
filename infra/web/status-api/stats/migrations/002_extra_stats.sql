-- Streaks, multi kills, first blood, time played and who killed whom, per stats line (the server writes them from 'v1' lines on;
-- older lines simply have zeros).  Streaks are a player's best over the round (or, in stopwatch round 2, over both rounds), so the
-- page takes the maximum of them, not the sum.

ALTER TABLE game_players
    ADD COLUMN team_gibs    int NOT NULL DEFAULT 0,
    ADD COLUMN goombas      int NOT NULL DEFAULT 0,
    ADD COLUMN poisons      int NOT NULL DEFAULT 0,
    ADD COLUMN knife_kills  int NOT NULL DEFAULT 0,
    ADD COLUMN kill_streak  int NOT NULL DEFAULT 0,
    ADD COLUMN death_streak int NOT NULL DEFAULT 0,
    ADD COLUMN double_kills int NOT NULL DEFAULT 0,
    ADD COLUMN triple_kills int NOT NULL DEFAULT 0,
    ADD COLUMN quad_kills   int NOT NULL DEFAULT 0,
    ADD COLUMN first_blood  int NOT NULL DEFAULT 0,
    ADD COLUMN first_death  int NOT NULL DEFAULT 0,
    ADD COLUMN play_ms      int NOT NULL DEFAULT 0;

-- Enemy kills by victim (slot and the victim's name at the time) for one stats line.
CREATE TABLE game_player_victims (
    player_id   bigint NOT NULL REFERENCES game_players(id) ON DELETE CASCADE,
    victim_slot int    NOT NULL,
    victim_name text   NOT NULL,
    kills       int    NOT NULL,
    PRIMARY KEY (player_id, victim_slot)
);
