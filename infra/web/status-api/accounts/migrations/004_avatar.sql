-- The profile picture an account chose: one of the class portraits from the limbo menu, "<class>_<team>"
-- (e.g. medic_axis).  Empty until chosen.
ALTER TABLE accounts ADD COLUMN avatar text NOT NULL DEFAULT '';
