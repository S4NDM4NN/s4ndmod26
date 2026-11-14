-- Whether the site pulls this server's game stats from its agent (an admin can turn it off, and purge what was pulled).
ALTER TABLE servers ADD COLUMN stats boolean NOT NULL DEFAULT true;
