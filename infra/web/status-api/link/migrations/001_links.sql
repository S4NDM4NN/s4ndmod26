-- Player keys tied to accounts.  All of it goes with the account: deleting an account unlinks its keys (the stats stay, under the key).
CREATE TABLE link_codes (
    hash       text        PRIMARY KEY,                       -- of the code: single use, short lived
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    server_id  text        NOT NULL DEFAULT '',               -- an automatic code is for one server ('' = typed by hand)
    expires_at timestamptz NOT NULL
);
CREATE INDEX link_codes_account ON link_codes (account_id, server_id);

-- a key that asked (by a typed code) and awaits its owner's confirmation
CREATE TABLE link_pending (
    key        text        PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    server_id  text        NOT NULL DEFAULT '',
    asked_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX link_pending_account ON link_pending (account_id);

CREATE TABLE player_links (
    key        text        PRIMARY KEY,                       -- the player's key (the id in the stats lines)
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    linked_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX player_links_account ON player_links (account_id);
