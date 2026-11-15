# Game stats (Postgres)

The game server writes per-game stats (`g_statsFile`, default on) to `<g_statsPath>/<match>.jsonl` — one JSON line per player
per round end, disconnect or map end, named after the replay (`replays_20261017_012254`). The status API ingests those files
into Postgres and serves them to the games pages:

    game server ──> stats/<match>.jsonl ──> status-api (this package) ──> Postgres ──> /api/games/<match>/stats ──> /game.html?r=<match>

Games are tracked on their own for now: there is no player identity across games yet.

## Running it

| Setting | Default | |
|---|---|---|
| `DATABASE_URL` | *(unset)* | `postgres://user:pass@host:5432/db`. Unset: stats are off, the stats endpoint answers 503 and the games list shows no Stats links. |
| `STATS_DIR` | `/usr/share/nginx/html/downloads/s4ndmod26/stats` | where the server's `.jsonl` files appear (the web container sees the game server's volume there) |

The schema is applied at startup (`migrations/`, numbered, tracked in `schema_migrations`; several instances starting together is
fine). Ingest runs every 15 s: it remembers how far it has read each file, only takes whole lines, and stores every line at most
once (keyed by its byte offset), so a restart or a second instance never duplicates anything. A bad line is logged and skipped.

`docker compose up` starts a Postgres next to the web container with matching settings.

### Kubernetes

The web pod needs a Postgres and `DATABASE_URL`. The pattern used by the other apps (`umami/postgres.yaml`) works: a
StatefulSet with `postgres:17-alpine`, a headless Service, a password in a SOPS secret, then
`DATABASE_URL=postgres://s4nd:$PASSWORD@s4ndmod26-postgres:5432/s4nd?sslmode=disable` on the web container. Preview (PR)
environments can use an `emptyDir` volume: the stats are rebuilt from the files, which are the source of truth.

## Tables

- `games` — one per match (`match` = the replay name, `map`, `started_at` from the name, taken as UTC).
- `game_players` — one row per stats line: that player's totals for one round (a stopwatch game has two rounds; the page adds
  them up). Players who leave and rejoin appear as several rows.
- `game_player_weapons` — OSP's five numbers per weapon per row (hits, attempts, kills, deaths, headshots). Packs and the
  syringe are rows too (packs: hits = used, attempts = dropped; syringe: hits = revives); `World` holds deaths with no weapon.
- `ingest_files` — how far into each file has been read.

## Tests

`go test ./...` runs everything that needs no database (line parsing, top shots). The Postgres tests (schema, ingest,
queries) run when `STATS_TEST_DSN` is set; `tools/local/stats_test.sh` starts a throwaway Postgres (downloaded once into
`~/.cache/s4ndmod26-pg`) and runs them.
