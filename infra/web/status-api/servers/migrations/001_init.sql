-- Community game servers: registered by an account with host_server, probed from outside, approved by an admin.
CREATE TABLE servers (
    id           bigserial   PRIMARY KEY,
    server_id    text        NOT NULL UNIQUE,      -- the agent's id, derived from its key
    public_key   bytea       NOT NULL,
    owner_id     bigint      REFERENCES accounts(id) ON DELETE SET NULL,
    name         text        NOT NULL,
    game_addr    text        NOT NULL UNIQUE,      -- host:port, lower case: where the game answers over UDP
    wss_url      text        NOT NULL UNIQUE,      -- wss://host[:port]/ws, lower case: where browsers connect
    status       text        NOT NULL DEFAULT 'pending',   -- pending | approved | blocked
    official     boolean     NOT NULL DEFAULT false,       -- runs inside our cluster: not probed from outside
    created_at   timestamptz NOT NULL DEFAULT now(),
    decided_at   timestamptz,
    decided_by   bigint,
    last_claim_at timestamptz,
    udp_ok       boolean     NOT NULL DEFAULT false,
    udp_error    text        NOT NULL DEFAULT '',
    udp_checked  timestamptz,
    ws_ok        boolean     NOT NULL DEFAULT false,
    ws_error     text        NOT NULL DEFAULT '',
    ws_checked   timestamptz,
    last_ok_at   timestamptz,                      -- the last time both probes passed
    hostname     text        NOT NULL DEFAULT '',  -- from the last good status query
    map          text        NOT NULL DEFAULT '',
    players      int         NOT NULL DEFAULT 0,
    max_players  int         NOT NULL DEFAULT 0
);
CREATE INDEX servers_owner ON servers (owner_id);
CREATE INDEX servers_status ON servers (status, id DESC);

-- One-time registration tokens: only a hash is kept.  A token makes one registration, then is gone.
CREATE TABLE server_tokens (
    token_hash bytea       PRIMARY KEY,
    account_id bigint      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX server_tokens_account ON server_tokens (account_id);
