# Build Graph

## Authoritative workflow

Use Docker as the top-level build and runtime entrypoint.

- `docker compose build`
- `docker compose up -d --build`
- `docker build --target mod-package --output type=local,dest=./mod-out .`

Direct `make`, `bjam`, and `cmake` invocations are subsystem internals used by Docker stages.

## Source roots to outputs

- `engine/iortcw`
  - builds `iowolfded.x86_64`
  - builds downloadable iortcw clients
- `mod/rtcw`
  - builds `qagame`, `cgame`, and `ui` modules for Linux/Windows
  - provides `main/ui_mp` assets for `s4ndmod26.pk3`
- `bot/omnibot`
  - builds `omnibot_rtcw.x86_64.so`
- `assets/rtcw`
  - provides `ob_media.pk3`
  - provides Omni-bot scripts, nav, and global scripts
  - provides `changelog.txt`
- `third_party/zlib`
  - replay/archive compression dependency for game-module builds
- `infra/docker`
  - server runtime config
- `infra/web`
  - nginx download frontend
  - status API source
  - installer scripts

## Compatibility paths

Old historical paths still exist as symlinks so older relative includes and scripts do not break during the transition:

- `iortcw`
- `0.83/GameInterfaces/RTCW`
- `0.83/Installer/Files/rtcw`
- `0.83/Omnibot/dependencies/physfs/zlib123`
- `docker`
- `web`

These should not be used as the primary orientation docs for new work.
