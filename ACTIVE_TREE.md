# Active Tree

Default development should stay inside these roots:

- `engine/`
  - vendored `iortcw` source used by Docker builds
- `mod/`
  - active RTCW qagame/cgame/ui source and mod-facing assets
- `bot/`
  - Omni-bot active source entrypoint
- `assets/`
  - runtime content the project ships directly
- `third_party/`
  - active vendored dependencies owned in-tree
- `infra/`
  - Docker, compose-adjacent config, and web/status services

These paths are compatibility surfaces, not the canonical roots:

- `iortcw/`
- `0.83/GameInterfaces/RTCW`
- `0.83/Installer/Files/rtcw`
- `docker/`
- `web/`

`legacy/` is archive material. Do not start there unless the active tree does not answer the question.
