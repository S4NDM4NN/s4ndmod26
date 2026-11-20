# What s4ndmod26 actually stores (inventory for the privacy pass)

Written 2026-11-14 from the code on `main` (37ea50b4) and the gitops overlays.  Facts only; nothing here is legal advice.
"Verified" = read in code/config.  "Unknown" = depends on something I cannot see from the repo.

## 1. Where a player's data lives

| Where | What | Keyed by | Kept how long | Public? |
| --- | --- | --- | --- | --- |
| Game server stats files (`<mod>/stats/*.jsonl`, an ingest buffer, not the record) | one JSON line per player per round: name, key (`pid`), team, kills, deaths, weapons, play time | key + name | **30 days** (the agent's replay scanner loop deletes `*.jsonl` older than `AGENT_STATS_MAX_AGE_DAYS`, default 30, -1 keeps; s4ndmod26 PR #148) | served by the agent to the site (`/v1/stats`) |
| Postgres `game_players` (+ `game_player_weapons`, `game_player_victims`) | the same lines, one row per player per round; victims by slot and **name** | `pid`, `name`, `name_raw` | forever | yes: `/api/players/{key}`, `/api/games/...`, `player.html` |
| Postgres `game_events` | kills/revives/etc. from the replay feed: **actor and target names**, slots, team, weapon, kill position (`pos_x/y/z`) | name + slot (no key) | forever | yes (game page, timeline) |
| Postgres `replay_index.summary` (jsonb) | per replay: every player name (`players`), POTG player, highlight names | name | forever | yes (games list search) |
| Postgres `player_cache` | derived totals per key, names used (aliases), pools | `pid` | rebuilt whenever stale | yes (player page) |
| Game server replays (`.rpl`, `.txt`, `.json`, `.summary.json`) | movement, positions, kills, **names**; no IPs or guids found (verified `g_replay.c` records names only) | name/slot | agent default **30 days** all files (`AGENT_REPLAY_MAX_AGE_DAYS`); legacy web scanner default **10 days**; dev adds 1 day for bot-only games; configurable per server | yes while they exist (3D viewer) |
| Postgres `accounts` | username (public), **email** (plain text + normalized copy), argon2id password hash, avatar, created, suspension | account id | until the account is deleted | username public, email never |
| `account_sessions`, `account_tokens`, `device_logins` | hashed session/email tokens; launcher sign-ins incl. **the requester's IP** | account id | sessions 30 days (link-only sessions 1 h); tokens 1-48 h; device logins 10 min, but expired rows are only swept when the next sign-in starts | no |
| `player_links` (+ `link_codes`, `link_pending`) | account <-> key, private label ("Chrome on Windows", computer name) | key + account | until removed / account deleted | the link itself is public (a key shows the account's username and avatar); labels are private |
| `account_audit` | admin actions with **actor and target usernames as text** | text | forever; **survives account deletion** (names are copied, not referenced) | admin only |
| `servers`, `server_tokens` | a host's server address, claim token | account id | until removed | listed servers: public address |
| Client demos (uploads, `demos` tables + S3 bucket in prod) | uploaded `.dm_60` demos, owner = account | account | until deleted by owner/admin | public pages |
| Browser (client side) | identity seed in IndexedDB (`identity.dat`); `localStorage` username/avatar of the signed-in account; cookie `s4nd_session` (HttpOnly, Lax, only when signed in) | - | until the player clears them | - |
| Launcher (client side) | `<config>/s4ndmod-launcher/account.json` (username, key -> owner map) and kept keys `keys/<pub>.dat`; `identity.dat` | - | until the player deletes them | - |

## 2. IP addresses and logs
- **Site code**: IPs are used for rate limits **in memory only** (login, register, reset, device sign-in), plus `device_logins.ip` (above).  Nothing else in Postgres stores an IP (verified by grep).
- **nginx**: no `access_log` directive in `nginx.conf`, so the image default applies (access log to the container's stdout, with the address the ingress passes).  **Unknown**: how long the cluster keeps container logs.
- **Game server console**: a Quake 3 engine normally prints connecting players (name and IP address) to its console, which goes to the container log.  **Not checked in this build**, and the log retention is unknown.
- **Mail**: until a mail provider exists the `LogMailer` writes **the whole mail (recipient, verification/reset link) to the container log** (`accounts/mail.go`).  Accounts are off in dev/prod, so nothing is logged yet.
- **Analytics**: a self-hosted Umami (`stats.s4ndmod.com`, script on the pages above; cookieless by design, but **not reviewed** which fields it keeps).

## 3. Findings that change the design
1. **Names are everywhere, keys are not.**  `game_events`, `game_player_victims`, `replay_index.summary` and the replay files identify people by *name only*.  Blanking `pid` on `game_players` does not erase a person from those.  Erasure has to rewrite names too, matched by (game, slot, the player's name(s) in that game).
2. **The database is the record and is backed up** (restic to Garage on dal02, daily, keep 7 daily / 4 weekly / 6 monthly; `gitops docs/backups.md`).  It is no longer rebuilt from the stats files, which are deleted after 30 days.  Consequences:
   - **Backups keep erased data.**  An erasure done in Postgres stays in older snapshots until they age out (up to about 6 months).  Policy to state: erased in the live database at once, gone from backups when the snapshots expire; a restore from backup must re-apply erasures (see the tombstone in section 4).
   - **Accounts and links live in the same database** and are covered by the same backup, so an erased account can come back from a restore unless the tombstone/erasure log is re-applied.
3. **The DB is kept forever**; replays and the raw stats files expire (30 days).  A retention rule for per-game rows with a key (for example: keep rows, drop the key after N months of inactivity) is a policy decision, not code yet.
4. `account_audit` keeps usernames after an account is deleted; `device_logins` keeps IPs until the next sign-in sweeps them.  Both are small fixes.
5. Public by design (decided): usernames, player pages by key, game history, the key -> username link.
6. Community servers (later): their stats files and replays stay on their machines; our DB holds the pulled rows tagged `server_id`, and `PurgeServer` already removes a whole server.  A key-level erasure must also cover rows from community servers, and the files on those servers are outside our control.

## 4. Proposed erasure design (for review)
Per key (and per account = all its keys):
1. Authenticate: the person signs a one-time code with the key (the same proof the link uses), or an admin acts after an out-of-band check.  No account needed.
2. In one transaction: for every `game_players` row of the key: remember (game_id, slot, name, name_raw); then set `pid = ''`, `name = name_raw = 'Deleted Player'`; rename matching `game_events` actor/target names and `game_player_victims.victim_name` and the names in `replay_index.summary` for those games; delete `player_cache` for the key; delete `player_links` (and the account if requested).
3. Other players' kills, deaths and results stay (the rows still exist, anonymized).  Aggregates are recomputed by the existing stale-marking.
4. Re-ingest guard: store a **tombstone** `erased_keys(hash)` = SHA-256 of the key (not the key, not the name, no date of birth of anything else) and have ingest blank `pid`/names of a tombstoned key.  Honest trade-off: a hash of a public key is still checkable by whoever holds that key, but it is not personal data on its own and it is the smallest thing that stops a restore from an old backup (or a re-ingest) from resurrecting the profile.  Keep the tombstone list outside the backup rotation, or re-apply it after any restore.
5. Official servers' stats files: they expire after 30 days, so a rewrite is optional (an admin-only agent rewrite or a one-off operator tool would shorten that window; the tombstone already blanks the key at ingest).
6. Replays: they expire (30 days by default).  An erasure request can also delete the replays of games the key played (admin tool), or we accept expiry and say so in the policy.
