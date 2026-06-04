# s4ndmod26

A self-hosted **Return to Castle Wolfenstein** server and client distribution. One `docker compose up` starts the game server and a web frontend that serves status, downloads, and one-line installers for players.

---

## What's Inside

| Component | Source | Notes |
|---|---|---|
| iortcw server + client | `engine/iortcw/` | 1.51d, all platforms |
| Bot AI library | `bot/omnibot/` | Omni-bot 0.93, CMake |
| Game interface (qagame/cgame/ui) | `mod/rtcw/src/` | bjam, all platforms |
| Bot scripts + waypoints | `assets/rtcw/` | 315 maps covered |
| Web frontend | `infra/web/` | nginx + Go status API |
| Base game data | `~/rtcw/main/` locally, `s4ndmod.com/downloads/main/` fallback | Local compose mounts your installed paks; image build still has a download fallback |

---

## Quick Start (Server)

```bash
git clone <repo-url>
cd s4ndmod26
docker compose up -d --build
```

- Game server: `udp/:27960`
- Web frontend: `http://localhost` — status, downloads, install instructions

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
mod-package                         scratch stage — all distributable files
```

To build and export all distributable files:
```bash
docker build --target mod-package --output type=local,dest=./mod-out .
```

---

## Configuration

### Server — `infra/docker/server.cfg`

| Cvar | Default | Description |
|---|---|---|
| `sv_hostname` | `iortcw Omnibot Server` | Name shown in server browser |
| `sv_maxclients` | `20` | Max players + bots combined |
| `sv_pure` | `0` | Pure server check (see note below) |
| `g_userAxisRespawnTime` | `10` | Axis respawn time (seconds) |
| `g_userAlliedRespawnTime` | `10` | Allied respawn time (seconds) |
| `rconpassword` | *(empty)* | **Set this before exposing to the internet** |

> **sv_pure note:** Currently `0` because loading custom cgame/ui from loose files requires either `sv_pure 0` or an iortcw build with `STANDALONE=1`. To restore pure server checking: rebuild with `STANDALONE=1` added to the iortcw Makefile flags in both `iortcw-builder` and `iortcw-client-*` stages.

### Bot count — `assets/rtcw/scripts/rtcw_autoexec.gm`

```gm
Server.MinBots = 8;   // auto-fill to this number
Server.MaxBots = 8;   // kick bots when humans join above this
```

Bot count can't be set in `server.cfg` — Omnibot hasn't loaded yet when that file executes.

---

## Adding Maps

Mount extra map pk3s at runtime without rebuilding:

```bash
# drop pk3s into ./maps/, then:
docker compose up -d
```

The compose file mounts `./maps/` into `/rtcw/main/maps/` automatically.
It also mounts `${HOME}/rtcw/main` into both containers so local rebuilds can reuse your installed `pak0.pk3` and `mp_pak0-5.pk3` instead of redownloading them.

---

## Updating iortcw

iortcw source is vendored in `engine/iortcw/` (with a compatibility symlink at `iortcw/`). To pull upstream changes:

```bash
git clone --depth=1 https://github.com/iortcw/iortcw.git /tmp/iortcw-upstream
rsync -av --delete /tmp/iortcw-upstream/ engine/iortcw/
git add engine/iortcw/ iortcw && git commit -m "iortcw: sync from upstream"
```

---

## Directory Layout

```
s4ndmod26/
├── engine/
│   └── iortcw/                      ← vendored iortcw source (server + client)
├── mod/
│   └── rtcw/
│       ├── src/                     ← qagame/cgame/ui source (bjam)
│       └── main/                    ← mod-side UI/config assets
├── bot/
│   └── omnibot/                     ← active Omni-bot source entrypoint
├── assets/
│   └── rtcw/
│       ├── scripts/                 ← GameMonkey bot scripts
│       ├── nav/                     ← waypoints + goal files (315 maps)
│       ├── global_scripts/          ← shared Omni-bot utilities
│       └── game/ob_media.pk3        ← bot skins/sounds
├── third_party/
│   └── zlib/                        ← owned zlib source for replay/archive builds
├── infra/
│   ├── docker/
│   │   └── server.cfg               ← server configuration
│   └── web/
│       ├── nginx/html/              ← index.html, install.sh, install.ps1
│       ├── status-api/              ← Go UDP status poller
│       └── entrypoint.sh
├── legacy/                          ← archived non-default project material
├── maps/                            ← volume-mounted extra map pk3s
├── Dockerfile                       ← all build + runtime stages
└── docker-compose.yml               ← two services: rtcw-server + web
```

Compatibility symlinks remain in place for older paths such as `iortcw/`, `0.83/GameInterfaces/RTCW`, `0.83/Installer/Files/rtcw`, `docker/`, and `web/`, but new work should start from the active roots above.

Omni-bot's `gmscriptex` dependency is vendored directly in-tree under `0.83/Omnibot/dependencies/gmscriptex`; there is no submodule bootstrap step anymore.

## Runtime Layout (server container)

```
/rtcw/
├── iowolfded.x86_64
├── main/
│   └── pak0.pk3, mp_pak0–5.pk3      ← base game (~370 MB)
├── s4ndmod26/
│   ├── qagame.mp.x86_64.so          ← server game module (Omnibot hooks)
│   ├── cgame.mp.x86_64.so           ← client module (for connecting Linux clients)
│   ├── ui.mp.x86_64.so
│   └── s4ndmod26.pk3                ← mod content (shaders, sounds, HUD)
└── omni-bot/
    ├── omnibot_rtcw.x86_64.so
    ├── global_scripts/
    └── rtcw/
        ├── scripts/
        └── nav/
```

---

## Known Limitations

- **No map rotation** — server stays on `mp_beach`. Add a rotation to `server.cfg` or use a restart script.
- **sv_pure 0** — pure server checking is disabled until a `STANDALONE=1` iortcw build is done (see configuration note above).
- **Linux client only** — the Windows client build is cross-compiled via MinGW and served for download, but hasn't been tested end-to-end on a real Windows machine yet.
- **Incomplete navs** — 315 maps have full waypoints; maps in `assets/rtcw/incomplete_navs/` have partial coverage and bots may not path correctly.
- **No rcon password** — set one in `infra/docker/server.cfg` before exposing to the internet.
