-- The farthest hitscan kill of a player's round: distance in game units, the weapon and the victim's name.  Zero distance = none.
ALTER TABLE game_players
    ADD COLUMN longest_kill        int  NOT NULL DEFAULT 0,
    ADD COLUMN longest_kill_weapon text NOT NULL DEFAULT '',
    ADD COLUMN longest_kill_victim text NOT NULL DEFAULT '';
