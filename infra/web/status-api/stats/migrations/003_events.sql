-- The game's event feed (kills, revives, objectives, joins and leaves), taken from the replay analysis once when it appears, so
-- it is still there after the replay itself is pruned.
CREATE TABLE game_events (
    game_id      bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    seq          int    NOT NULL,                 -- order within the game
    t_ms         int    NOT NULL,                 -- since the recording started
    type         text   NOT NULL,                 -- kill | teamkill | suicide | revive | plant | defuse | capture | steal | return | denial | join | leave | end
    actor_slot   int    NOT NULL DEFAULT -1,
    actor_name   text   NOT NULL DEFAULT '',
    actor_team   text   NOT NULL DEFAULT '',      -- axis | allies | ''
    target_slot  int    NOT NULL DEFAULT -1,
    target_name  text   NOT NULL DEFAULT '',
    target_team  text   NOT NULL DEFAULT '',
    weapon       text   NOT NULL DEFAULT '',
    headshot     boolean NOT NULL DEFAULT false,
    PRIMARY KEY (game_id, seq)
);
