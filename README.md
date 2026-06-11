# s4ndmod26

A self-hosted **Return to Castle Wolfenstein** server and client distribution. One `docker compose up` starts the game server and a web frontend that serves status, downloads, one-line installers, and a live/replay timeline viewer.

---

## What's Inside

| Component | Source | Notes |
|---|---|---|
| iortcw server + client | `iortcw/` | 1.51d, all platforms |
| Bot AI library | `omnibot/` | Omni-bot 0.93, CMake |
| Game interface (qagame/cgame/ui) | `mod/src/` | bjam, all platforms |
| Bot scripts + waypoints | `assets/` | 315 maps covered |
| Web frontend | `infra/web/` | nginx + Go status API |
| Base game data | `gamedata/main/` | Mounted at runtime; see Quick Start |

---

## Quick Start (Server)

```bash
git clone <repo-url>
cd s4ndmod26

# Populate gamedata/ on first run (copies server.cfg, pk3, game module):
docker build --target gamedata --output type=local,dest=./gamedata .

# Drop your retail RTCW pak files into gamedata/main/ (or let the image
# download fallback handle it — slower first start):
#   gamedata/main/pak0.pk3  mp_pak0.pk3 … mp_pak5.pk3

docker compose up -d
```

- Game server: `udp/:27960`
- Web frontend: `http://localhost` — status, downloads, install instructions, timeline

The server writes replays and runtime state into `gamedata/s4ndmod26/`, which is bind-mounted into the container so it persists across restarts.

---

## Playing (Client)

### Linux
```bash
curl -sL http://s4ndmod.com/install.sh | bash
~/rtcw/iowolfmp.x86_64 +set fs_basepath ~/rtcw +set fs_game s4ndmod26 +connect s4ndmod.com
```

### Windows (PowerShell)
```powershell
iwr -useb http://s4ndmod.com/install.ps1 | iex
~\wolf\ioWolfMP.x64.exe +set fs_basepath ~\wolf +set fs_game s4ndmod26 +connect s4ndmod.com
```

Both installers download the iortcw client, base game paks, and the mod. Custom install path: set `RTCW_DIR` (Linux) or `$env:RTCW_DIR` (Windows) before running.

> **Note:** The retail RTCW client is supported — the mod pk3 includes 32-bit cgame/ui modules so the original client can load the mod without iortcw.

---

## Build Stages

The Dockerfile is multi-stage with full layer caching:

```
game-src-linux / game-src-windows   compiler base images
game-linux-64 / game-linux-32       qagame/cgame/ui .so (bjam)
game-win-64 / game-win-32           qagame/cgame/ui .dll (MinGW cross-compile)
omnibot-lib-builder                 omnibot_rtcw.x86_64.so (CMake)
pk3-builder                         s4ndmod26.pk3 (content + client modules)
iortcw-builder                      iowolfded.x86_64 (server binary)
iortcw-client-linux-64              iowolfmp.x86_64 + renderer .so
iortcw-client-windows-64            ioWolfMP.x64.exe + renderer .dll + SDL/OpenAL
status-api-builder                  Go status API binary
runtime                             final server image
web                                 nginx + status API + all downloads
gamedata                            scratch stage — seeds gamedata/ on first run
```

To seed `gamedata/` on a fresh checkout:
```bash
docker build --target gamedata --output type=local,dest=./gamedata .
```

---

## Configuration

### Server — `gamedata/s4ndmod26/server.cfg`

This file lives in the bind-mounted `gamedata/s4ndmod26/` directory so edits take effect on the next server restart without a rebuild.

| Cvar | Default | Description |
|---|---|---|
| `sv_hostname` | `iortcw Omnibot Server` | Name shown in server browser |
| `sv_maxclients` | `20` | Max players + bots combined |
| `sv_pure` | `0` | Pure server check (see note below) |
| `g_userAxisRespawnTime` | `10` | Axis respawn time (seconds) |
| `g_userAlliedRespawnTime` | `10` | Allied respawn time (seconds) |
| `rconpassword` | *(empty)* | **Set this before exposing to the internet** |

> **sv_pure note:** Currently `0` because loading custom cgame/ui from loose files requires either `sv_pure 0` or an iortcw build with `STANDALONE=1`. To restore pure server checking: rebuild with `STANDALONE=1` added to the iortcw Makefile flags in both `iortcw-builder` and `iortcw-client-*` stages.

### Bot count — `assets/scripts/rtcw_autoexec.gm`

```gm
Server.MinBots = 8;   // auto-fill to this number
Server.MaxBots = 8;   // kick bots when humans join above this
```

Bot count can't be set in `server.cfg` — Omnibot hasn't loaded yet when that file executes.

---

## Adding Maps

Mount extra map pk3s at runtime without rebuilding:

```bash
# drop pk3s into ./gamedata/maps/, then:
docker compose up -d
```

The compose file mounts `./gamedata/maps/` into `/rtcw/main/maps/` automatically.
Drop your retail paks into `./gamedata/main/` and compose will mount them instead of triggering a download fallback.

---

## Updating iortcw

iortcw source is vendored in `iortcw/`. To pull upstream changes:

```bash
git clone --depth=1 https://github.com/iortcw/iortcw.git /tmp/iortcw-upstream
rsync -av --delete /tmp/iortcw-upstream/ iortcw/
git add iortcw/ && git commit -m "iortcw: sync from upstream"
```

---

## Directory Layout

```
s4ndmod26/
├── iortcw/                          ← vendored iortcw source (server + client)
├── mod/
│   └── src/                         ← qagame/cgame/ui source (bjam)
├── omnibot/                         ← Omni-bot source (CMake)
├── assets/
│   ├── scripts/                     ← GameMonkey bot scripts
│   ├── nav/                         ← waypoints + goal files (315 maps)
│   ├── global_scripts/              ← shared Omni-bot utilities
│   └── game/ob_media.pk3            ← bot skins/sounds
├── third_party/
│   └── zlib/                        ← zlib source for replay compression + omnibot physfs
├── infra/
│   ├── server-entrypoint.sh         ← server container entrypoint
│   ├── entrypoint.sh                ← web container entrypoint
│   └── web/
│       ├── nginx/html/              ← index.html, install.sh, install.ps1, timeline.html, …
│       ├── status-api/              ← Go status + replay API
│       └── supervisord.conf
├── gamedata/                        ← bind-mounted host volume (persists across restarts)
│   ├── main/                        ← base game paks (pak0.pk3, mp_pak*.pk3)
│   └── s4ndmod26/
│       ├── server.cfg               ← server configuration (edit here)
│       ├── s4ndmod26.pk3            ← mod pk3 (rebuilt by Docker)
│       ├── qagame.mp.x86_64.so      ← server game module (rebuilt by Docker)
│       └── replays/                 ← replay files + pre-computed JSON analysis
├── legacy/                          ← archived non-active project material
├── Dockerfile                       ← all build + runtime stages
└── docker-compose.yml               ← two services: rtcw-server + web
```

Omni-bot's `gmscriptex` dependency is vendored directly in-tree under `omnibot/dependencies/gmscriptex`; there is no submodule bootstrap step.

---

## Runtime Layout (server container)

```
/rtcw/
├── iowolfded.x86_64
├── main/
│   └── pak0.pk3, mp_pak0–5.pk3      ← base game (~370 MB)
└── s4ndmod26/
    ├── qagame.mp.x86_64.so          ← server game module (Omnibot hooks)
    ├── cgame.mp.x86_64.so           ← client module (for connecting Linux clients)
    ├── ui.mp.x86_64.so
    ├── s4ndmod26.pk3                ← mod content (shaders, sounds, HUD)
    ├── server.cfg                   ← bind-mounted from gamedata/s4ndmod26/
    └── replays/                     ← replay files + JSON, bind-mounted from gamedata/
```

The web container also mounts `gamedata/s4ndmod26/` so the status API and nginx can serve replay files directly from the same directory the server writes into.

---

## Web Frontend

- `/` — server status and player list
- `/games.html` — recent game history
- `/live.html` — live match timeline (SSE stream)
- `/replay.html` — replay browser
- `/timeline.html?mode=replay&r=<name>` — full SVG timeline for a completed match
- `/downloads/` — client installers, mod pk3, base game paks

---

## Known Limitations

- **No map rotation** — server stays on `mp_beach`. Add a rotation to `server.cfg` or use a restart script.
- **sv_pure 0** — pure server checking is disabled until a `STANDALONE=1` iortcw build is done (see configuration note above).
- **Linux client only** — the Windows client build is cross-compiled via MinGW and served for download, but hasn't been tested end-to-end on a real Windows machine yet.
- **Incomplete navs** — 315 maps have full waypoints; maps in `assets/incomplete_navs/` have partial coverage and bots may not path correctly.
- **No rcon password** — set one in `gamedata/s4ndmod26/server.cfg` before exposing to the internet.
