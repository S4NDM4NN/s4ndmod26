-- Keys whose owners asked to be erased.  Only a hash of the key: enough to stop a rebuild of this database (the game servers'
-- stats files still hold the key) from bringing the profile back, and nothing that says who it was.
CREATE TABLE erased_keys (
    key_hash  bytea       PRIMARY KEY,   -- sha256 of the key text
    erased_at timestamptz NOT NULL DEFAULT now()
);
