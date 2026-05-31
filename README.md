# s4ndmod26 — iortcw + Omnibot Docker Server

A fully containerized **Return to Castle Wolfenstein** dedicated server running [iortcw](https://github.com/iortcw/iortcw) with [Omni-bot 0.93](https://github.com/jswigart/omni-bot) AI bots. One `docker build` and you're playing.

---

## What's Inside

| Component | Source | Version |
|---|---|---|
| Game server | [iortcw/iortcw](https://github.com/iortcw/iortcw) | 1.51d (latest) |
| Bot AI library | `0.83/Omnibot/` (this repo, stable branch) | 0.93 |
| Game interface | `0.83/GameInterfaces/RTCW/` (this repo) | custom |
| Base game data | [s4ndmod.com/downloads/main](http://s4ndmod.com/downloads/main) | — |

The game interface (`qagame.mp.x86_64.so`) is built from the source in this repo. It hooks into the Omni-bot library and gives bots full awareness of RTCW's game rules — objectives, classes, medics reviving, engineers planting, etc.

---

## Requirements

- Docker (20+, BuildKit enabled by default)
- ~500 MB disk space for the final image (pak files are ~370 MB)
- Port `27960/udp` open if you want LAN/internet access

---

## Quick Start

```bash
git clone https://repo.s4ndmod.com/S4NDMoD/s4ndmod26.git
cd s4ndmod26
git checkout stable
git submodule update --init

docker build -t rtcw-server .
docker run -d --name rtcw-server -p 27960:27960/udp rtcw-server
```

Connect from your iortcw client:
```
/connect <your-server-ip>
```

> **Client:** You need [iortcw](https://github.com/iortcw/iortcw/releases) — the original RTCW retail client won't work with the 64-bit game modules.

---

## Build Stages

The Dockerfile is multi-stage. Each stage is independently cached:

```
game-builder        builds qagame/cgame/ui .so via bjam
omnibot-lib-builder builds omnibot_rtcw.x86_64.so via CMake
iortcw-builder      clones and builds the iortcw dedicated server
runtime             assembles everything + downloads pak files
```

To rebuild just the game module after changing C++ code:

```bash
docker build --target game-builder -t rtcw-game .
```

---

## Configuration

### Server settings — `docker/server.cfg`

| Cvar | Default | Description |
|---|---|---|
| `sv_hostname` | `iortcw Omnibot Server` | Server name shown in browser |
| `sv_maxclients` | `20` | Max players + bots combined |
| `g_userAxisRespawnTime` | `10` | Axis spawn time in seconds |
| `g_userAlliedRespawnTime` | `10` | Allied spawn time in seconds |
| `rconpassword` | *(empty)* | Set this before exposing to internet |

### Bot count — `0.83/Installer/Files/rtcw/scripts/rtcw_autoexec.gm`

```gm
Server.MinBots = 8;   // bots auto-fill to this number
Server.MaxBots = 8;   // cap (bots kick out when humans join if above this)
```

> **Note:** Bot count cannot be set in `server.cfg` — Omnibot hasn't loaded yet when the cfg executes. The autoexec script runs after initialization.

After any config change, rebuild the runtime layer (fast, ~15 seconds — only the changed files are re-layered):

```bash
docker build -t rtcw-server . && docker stop rtcw-server && docker rm rtcw-server && docker run -d --name rtcw-server -p 27960:27960/udp rtcw-server
```

---

## Adding Maps

Extra maps from [s4ndmod.com/downloads/main](http://s4ndmod.com/downloads/main) can be mounted at runtime without a rebuild:

```bash
# Drop pk3 files into ./maps/ then:
docker run -d --name rtcw-server \
  -p 27960:27960/udp \
  -v $(pwd)/maps:/rtcw/main/maps:ro \
  rtcw-server
```

Or to bake them into the image, add `wget` lines for each pk3 in the runtime stage of the Dockerfile alongside the existing pak downloads.

---

## Directory Layout

```
s4ndmod26/
├── 0.83/
│   ├── GameInterfaces/RTCW/src/   ← qagame/cgame/ui source (bjam)
│   ├── Omnibot/                   ← bot AI library source (CMake)
│   └── Installer/Files/rtcw/
│       ├── scripts/               ← GameMonkey bot scripts
│       ├── nav/                   ← waypoints + goal files (315 maps)
│       ├── global_scripts/        ← cross-game utilities (from omni-bot 0.93 release)
│       └── game/ob_media.pk3      ← bot skins/sounds
├── docker/
│   └── server.cfg                 ← server configuration
├── Dockerfile                     ← multi-stage build
└── docker-compose.yml             ← convenience wrapper
```

---

## Runtime File Layout (inside container)

```
/rtcw/
├── iowolfded.x86_64               ← iortcw dedicated server binary
├── main/
│   ├── qagame.mp.x86_64.so        ← game module with Omnibot hooks
│   ├── cgame.mp.x86_64.so
│   ├── ui.mp.x86_64.so
│   ├── pak0.pk3                   ← base game assets (~302 MB)
│   ├── mp_pak0.pk3 – mp_pak5.pk3  ← multiplayer content
│   ├── ob_media.pk3               ← bot media
│   └── server.cfg
└── omni-bot/
    ├── omnibot_rtcw.x86_64.so     ← Omni-bot AI library
    ├── rtcw/
    │   ├── scripts/               ← bot GameMonkey scripts
    │   └── nav/                   ← waypoints (315 maps covered)
    └── global_scripts/            ← shared Omni-bot utilities
```

---

## Branch Notes

| Branch | Status | Notes |
|---|---|---|
| `stable` | ✅ Use this | CMake build, 64-bit, RTCW working |
| `master` | ⚠️ Do not use for Docker | Partially-migrated API, build broken for RTCW |

The `stable` branch uses CMake for the Omnibot library and a Boost.Build tag rule that automatically produces correctly named `*.mp.x86_64.so` files. The `master` branch has an API mismatch between the game interface and the Common headers that requires significant porting work.

---

## Submodule

The `gmscriptex` submodule URL in `.gitmodules` points to the public mirror:

```
https://github.com/jswigart/gmscriptex.git
```

(The original URL in the stable branch pointed to a private server — this has been updated.)

---

## Known Limitations

- **No map rotation yet** — server restarts on the same map (`mp_beach`). Map rotation needs either a rotation config or a restart script.
- **No rcon password set** — add one to `docker/server.cfg` before exposing to the internet.
- **Incomplete navs** — 315 maps have waypoints; others in `incomplete_navs/` have partial coverage. Bots will still join but may not path correctly on those maps.
