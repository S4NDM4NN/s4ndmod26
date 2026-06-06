# Active Tree

Default development should stay inside these roots:

- `iortcw/`
  - vendored `iortcw` source used by Docker builds
- `mod/`
  - active RTCW qagame/cgame/ui source and mod-facing assets
- `omnibot/`
  - Omni-bot active source entrypoint
- `assets/`
  - runtime content the project ships directly
- `third_party/`
  - active vendored dependencies owned in-tree
- `infra/`
  - Docker, compose-adjacent config, and web/status services

`gamedata/` mirrors the server's live RTCW directory layout:

- `gamedata/main/` — retail paks + custom map pk3s (gitignored; drop any pk3 here)
- `gamedata/s4ndmod26/` — live mod folder bind-mounted into the container:
  - `server.cfg` — tracked in git; edit and restart to change server settings
  - `*.so`, `*.pk3` — seeded from the image on first run (gitignored)
  - `replays/`, logs — written at runtime (gitignored)

nginx serves `gamedata/main/` at `/downloads/main/` so `sv_dlURL` in `server.cfg` points players at it for map downloads.

`legacy/` is archive material. Do not start there unless the active tree does not answer the question.
