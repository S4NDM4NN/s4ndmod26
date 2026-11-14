-- An admin can give one account more (or fewer) servers than the default.
CREATE TABLE server_limits (
  account_id bigint PRIMARY KEY,
  max_servers integer NOT NULL CHECK (max_servers >= 0 AND max_servers <= 1000)
);
