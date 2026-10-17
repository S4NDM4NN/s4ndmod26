-- Which team won each round, as the game server reported it on the round-end lines ("axis" / "allies").  A round the map script
-- gave no winner (a draw) has no row.  The game's winner is the last round's: in a stopwatch game the second round decides it.
CREATE TABLE game_rounds (
    game_id bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    round   int    NOT NULL,
    winner  text   NOT NULL,
    PRIMARY KEY (game_id, round)
);
