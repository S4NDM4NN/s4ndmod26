# Plan: sv_pure 1, versioned pk3 names, browser + retail support

Status: planning notes (not committed). Written 2026-10-15.

## Status (2026-10-15, uncommitted working tree)
Implemented and tested locally (native dedicated server + qagame built from this tree, browser
client from `tools/local/build_wasm.sh`, headless Chromium, `wasm/ws_udp_bridge.py`):
- **Browser joins an `sv_pure 1` server.** Client fetched `latest.txt`, downloaded the versioned pk3,
  loaded `ui.mp.wasm` and `cgame.mp.wasm` *from the pk3* ("Loading cgame dll from ...pk3"), got "on the
  pure list" for both paks, and reached the limbo menu on mp_beach.
- **Negative control:** server pk3 differing by one file -> client reports "not on the pure list" and
  fails ("Incorrect checksum for file: s4ndmod26/s4ndmod26_local.<crc>.pk3"; that is the crc-suffixed
  duplicate name the versioned scheme avoids).
- Not tested: a native Windows/Linux client (no SDL2/GL headers here), the Docker build itself
  (no Docker here), the nginx/installer scripts end to end (only `bash -n`).

Changes: Dockerfile (versioned pk3, wasm stages moved ahead of `pk3-builder`, wasm modules in pk3,
globs for every consumer, `latest.txt`), `server-entrypoint.sh` (cleanup + `latest.txt`), nginx
no-store for `latest.txt`, installers/start scripts (versioned name, no loose DLLs, `fs_homepath`),
`wasm_io.c` (reads `latest.txt`, prunes other pk3s, caches by name), `sv_pureVMPaths`
(+`%s_mp_x64.dll`, `%s.mp.wasm`), `gamedata/.../server.cfg` (`sv_pure 1`, public `sv_dlURL`),
`Makefile populate` cleanup, `tools/local/build_wasm.sh` + `replay_server.js` (versioned local pk3).
Prod `server.cfg` is the gitops ConfigMap (`sv_pure 0`); flip it there after the PR env test.
PR #26 (s4ndmod26) built successfully in CI (run 1038); the preview env was not reachable from this shell.

## Why
- Server runs `sv_pure 0` (`gamedata/s4ndmod26/server.cfg:4`).
  Unpure clients load **loose** cgame/ui DLLs from their homepath before looking in any pk3
  (`iortcw/code/qcommon/files.c:1571`, `FS_FindVM`). The DLL extracted from the pk3 then becomes
  the next stale loose file, so every update leaves clients on old code (stretched HUD bug,
  Windows `Documents\RTCW\s4ndmod26\cgame_mp_x64.dll` from June).
- Same-name pk3 downloads are saved as `s4ndmod26.<crc>.pk3`; nothing deletes old ones, and on an
  unpure server the alphabetically-last one wins.
- (Local compose only) the repo `server.cfg` has `sv_dlURL "http://127.0.0.1:99/downloads"`. Prod does not use it:
  the real `server.cfg` is the gitops ConfigMap `apps/s4ndmod26/base/configmap-servercfg.yaml`, where
  `sv_dlURL` is templated per environment and `sv_pure` is still 0.
- README / Dockerfile comments say sv_pure 1; the live config says 0.

## Step 1: versioned pk3 names
- Name: `s4ndmod26_<VERSION>.pk3` (VERSION already built by CI, e.g. `v26.10.14-9c8fdc9a`).
- Dockerfile `pk3-builder` produces the file; `runtime` stage copies it to `/rtcw/s4ndmod26-dist/`;
  `web` stage publishes it under `/downloads/s4ndmod26/`.
- `infra/server-entrypoint.sh`: delete `s4ndmod26_*.pk3` and legacy `s4ndmod26.pk3` from
  `/rtcw/s4ndmod26` before copying the new one (folder is bind-mounted, so old files persist).
- Keep a stable alias for scripts: `/downloads/s4ndmod26/latest.txt` containing the file name
  (until the launcher manifest replaces it).
- Update: `install.ps1`, `install.sh`, `start.ps1`, `start.sh`, zip-builder, `wasm` fs/shell, anything
  that hardcodes `s4ndmod26.pk3` (grep first).
- Optional later: split into `s4ndmod26_assets_v<N>.pk3` (rare) and the code pk3 (sorts after).

## Step 2: server config
- `sv_pure 1`
- `sv_dlURL "https://s4ndmod.com/downloads"` (public URL; confirm the path nginx serves).
- Fix README/Dockerfile comments.

## Step 3: browser client under sv_pure
Previous failure causes (`wasm/next_session_wasm_mp.md`):
1. pk3 bundled in `index.data` differed from the server's -> stop bundling; fetch the server's pk3
   through the existing in-browser pak download (`cl_main.c`, commit e10e2bad).
2. `cgame.mp.wasm` / `ui.mp.wasm` are loose in `wasm/fs/main/`, so under pure the client sends a
   `cp` string with no cgame/ui pak and `SV_VerifyPaks_f` (`server/sv_client.c:1338-1366`) kicks it.
   -> put the `.wasm` modules in the code pk3; add `"%s.mp.wasm"` (and `"%s_mp_x64.dll"`) to
   `sv_pureVMPaths` (`sv_client.c:1265`).
- Risk: `FS_CL_ExtractFromPakFile` (`files.c:1690`) then `dlopen` of a runtime-extracted wasm side
  module in MEMFS. If that fails, read module bytes from the pk3 directly.
- Test: local server with `sv_pure 1`, headless Chromium via `tools/local/replay_shots.js`-style
  harness, confirm no "unpure" drop.

## Step 4: native client verification
- Plant a stale loose `cgame_mp_x64.dll` + old `s4ndmod26.<crc>.pk3`; connect to a pure server;
  confirm the current module loads.

## Step 5 (later): retail pak support
- Works today under pure with `demopak0.pk3` present (retail paks are just hidden).
- To keep retail paks active: server holds retail paks in a separate dir; new cvar
  (e.g. `fs_optionalPakDir`) puts them in `FS_LoadedPakPureChecksums` (`files.c:4070`) but not in
  `FS_ReferencedPakNames/Checksums` (`files.c:4096-4135`), so they are allowed, never required,
  never offered for download. Pure checksums are salted per-map (`files.c:2359-2411`), so a static
  whitelist cannot work.
- Check `mp_beach.bsp` is identical in demopak0 vs retail mp_pak0; retail-only map-pack maps need a
  separate rotation.

## Open questions
- Public download URL/path for `sv_dlURL` (nginx serves `/downloads/` from `gamedata/`).
- Keep a loose-file fallback name for old clients that fetch `s4ndmod26.pk3`?
