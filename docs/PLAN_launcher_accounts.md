# Plan: S4NDMoD launcher, replays, accounts, montages

Status: planning notes (not committed). Written 2026-10-15. Depends on `PLAN_svpure_pk3.md` steps 1-2.

## Goals
1. Launcher/updater (Go, single exe): keep the install current and clean at launch.
2. Retail support: let the player point at their RTCW install and use full paks, still able to join
   s4ndmod.com.
3. Optional accounts: POTG history, local replay library.
4. Montages from POTG clips.

## Existing pieces
- Distribution: Dockerfile `zip-builder` (`s4ndmod26-windows.zip`), `web` stage (~439-498),
  `infra/web/nginx/html/install.ps1`/`install.sh`, `infra/web/start.ps1`/`start.sh`, `version.txt`.
- Replays: `mod/src/game/g_replay.c` (.rpl v8 + sidecar .txt), status-api `/api/replays`
  (`infra/web/status-api/main.go`), scanner pruning `.rpl` (`replay/scanner.go:63-109`).
- Browser MP4 export: `infra/web/nginx/html/play/replay_export.js` (single clip).
- Identity: engine `cl_guid` = MD5 of `wkey` (`iortcw/code/client/cl_main.c:1475`); spoofable, not
  recorded in replays.

## Phase A: manifest + launcher MVP
- `/downloads/manifest.json` generated in the Docker web stage:
  `{version, files:[{path, sha256, size, url, optional}]}` per platform.
- `launcher/` Go module: fetch manifest, hash-compare, download changed files (temp + atomic rename),
  remove any `s4ndmod26*.pk3` / loose DLL not in the manifest, launch
  `ioWolfMP.x64.exe +set fs_basepath <dir> +set fs_homepath <dir> +connect s4ndmod.com`, self-update.
- Portable install (homepath = install dir) so `Documents\RTCW` stale files never matter.
- Console progress UI first; richer UI later. Code signing later.

## Phase B: retail install support
- Detect Steam (app 9010 library folders) / GOG registry installs, or browse.
- Copy/hardlink `mp_pak0..5.pk3` (+ pak0) into `<install>\main\` after hash verification.
  Never download retail files; `demopak0.pk3` always installed.
- Needs server-side optional-pak support (see svpure plan step 5) for retail paks to stay active.

## Phase C: identity + local replays
- Add `guid_<slot>=<hashed cl_guid>` to the sidecar (`g_replay.c:2418-2427`) and `PlayerInfo`
  (`status-api/replay/analysis.go:136`).
- Launcher pulls the player's `.rpl`/`.txt` pairs into `<install>/replays/` (server prunes them) and
  plays via local `g_replayLoadFile`.

## Phase D: optional accounts
- SQLite in status-api; OAuth or magic-link login (no passwords).
- Link a guid via one-time code: website issues it, player types `/link CODE` in game, qagame calls
  the API. Fully optional; game/launcher/replays work without it.

## Phase E: montages
- Short term: playlist support in `replay_export.js` (several clips -> one MP4, title cards exist).
- Long term: headless render queue (Puppeteer, see `tools/local/replay_shots.js`) + ffmpeg stitching,
  surfaced on the account page.
- Native client has no `s4ndExport`; its `cl_avi.c` writes AVI, so desktop path needs ffmpeg.

## Open questions
- Launcher UI: console vs webview. Hosting/domain consolidation (`s4ndmod.com` vs `26.s4ndmod.com`).
- Linux/macOS launcher targets, or Windows only first?
