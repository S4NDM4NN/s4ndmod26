# AGENTS.md

Notes for AI agents (and people) working in this repo.  Keep this file current: when you learn a build/test step the hard way, add it here.

## Layout
- `mod/src/game` (qagame), `mod/src/cgame`, `mod/src/ui` — the mod.  Replay recorder/playback is `mod/src/game/g_replay.c`; its client side lives in `cgame` (`cg_servercmds.c` `replay_phase`, `cg_draw.c`, `cg_view.c`).
- `iortcw/` — engine, `wasm/` — browser client glue, `omnibot/` — bots, `infra/web/` — nginx pages + Go `status-api` (parses `.rpl` archives in `status-api/replay/parser.go`).
- Replay archives (`.rpl`, format v8) + sidecar `.txt` live in `gamedata/s4ndmod26/replays/` on a server and under `/downloads/s4ndmod26/replays/` on the web host.

## Quick compile check (seconds)
Syntax-check a changed mod file without a full build:
```bash
cd mod/src && gcc -fsyntax-only -w -DGAMEDLL -DCGAMEDLL -I game -I cgame -I ../../third_party/zlib game/g_replay.c
```

## Official builds (Docker)
`make build` / `make populate` (game modules into `gamedata/`), `make wasm-build` then `make wasm-deploy` (browser client).  Docker is not always installed in the dev shell (it isn't in the WSL session used so far) — use the local wasm build below.

## Local browser-client build (no Docker)
`tools/local/build_wasm.sh` mirrors the Dockerfile's wasm stage using a local emsdk (3.1.51 at `~/.local/share/emsdk`, override with `EMSDK_ENV`).
- Work dir: `WASM_WORK` (default `~/.cache/s4ndmod26-wasm`).  Output (client + `cgame/ui.mp.wasm`) goes to `$WASM_WORK/play`; the build tree is `$WASM_WORK/wb2` and is incremental (a rebuild after editing one file takes about a minute).
- One-time prerequisite: gl4es built for wasm into `$WASM_WORK/gl4es-opt/{lib,include}` — clone `https://github.com/ptitSeb/gl4es`, then `emcmake cmake -B build -DCMAKE_BUILD_TYPE=Release -DNOX11=ON -DNOEGL=ON -DSTATICLIB=ON "-DCMAKE_C_FLAGS=-sUSE_PTHREADS=1 -fPIC"`, build, copy `libGL.a` and `include/*` (same as the Dockerfile `gl4es-wasm` stage).
- The script needs `assets/game/ob_media.pk3` and python3; logs go to `$WASM_WORK/wasm_pass1.log` / `wasm_pass2.log` (grep them for `error`).
- The mod pk3 is versioned (`s4ndmod26_<VERSION>.pk3`, local build: `s4ndmod26_local.pk3` in `$WASM_WORK/dl` with `latest.txt`); the browser reads `/downloads/s4ndmod26/latest.txt` to find it, and `tools/local/replay_server.js` serves both.
- The game module (qagame, which holds `g_replay.c`) runs inside the browser too, in replay-server mode, so one build covers recorder and playback changes.

## Testing a replay in headless Chromium
1. Put a replay and its sidecar in `$T/replays/` (`NAME.rpl`, `NAME.txt`).  Prod archives: list with `curl https://s4ndmod26.dal01.s4ndmod.com/api/replays` (JSON; `potg_player`, `highlights`), download from `.../downloads/s4ndmod26/replays/NAME.{rpl,txt}`.
2. `S=$WASM_WORK T=<testdir> W=$WASM_WORK/wb2 DEMOPAK=<path to demopak0.pk3> node tools/local/replay_server.js &` (port 8123).
3. `T=<testdir> node tools/local/replay_shots.js NAME TAG 40 500` — screenshots to `$T/shots/`, console (incl. `[replay]` / `CG_Printf` output) to `$T/log_TAG.txt`.  Highlights: add `&player=N` as the 6th argument.  Load takes several seconds, then a ~3 s countdown, then the clip.
4. Set `g_replayDebug 1` for per-frame replay logging.

## Inspecting archives without the game
Archive v8: 2668-byte header, then chunks (24-byte header: startTime, endTime, frameCount, eventCount, uncompressed, compressed sizes + zlib payload).  Payload = frameCount, eventCount, then per frame `serverTime, sampleCount, samples[sampleSize]`, then events.  In a sample the `entityState_t` starts at byte 72 (`eType` +4, `eventSequence` +192, `events[4]` +196, `weapon` +232); `viewangles` at 384.  `status-api/replay/parser.go` is the reference.  Event enum values: see `bg_public.h` (`EV_FIRE_WEAPON` = 40, `ET_MISSILE` = 3, weapon ids `WP_*`).

## Optional text font (cg_s4ndfont)
- `mod/src/cgame/cg_s4ndfont.c`: `cg_s4ndfont 0|1|2` (game font / Courier Prime Bold / DejaVu Sans Mono Bold) draws the charset text (scoreboard, kill feed, chat, titles) with a baked font; `cg_s4ndfontSize` and `cg_s4ndfontSpace` tune it.  It hooks `CG_DrawStringExt`/`CG_DrawStringExt2` in `cg_drawtools.c`.
- Anything that centres, right-aligns or counts characters with `charWidth * strlen` must use `CG_S4ndFont_StrWidth()` / `CG_S4ndFont_Advance()` instead, or it sits off when the font is on (they return the old width when it is off).
- The engine looks baked fonts up by **point size only** (`fonts/fontImage_<size>.dat`), so the size is the font's identity: 30 Courier Prime Bold, 28 DejaVu Sans Mono Bold.  The menus use 12, 16, 17, 20, 24 - do not reuse them.  A stock engine has 6 font slots (`MAX_FONTS`, raised to 16 in our renderer) and the menus take 5, so only the selected font is registered.
- Bake a font with `tools/local/bakefont.py <ttf> <size> mod/main` (needs Pillow); `mod/main/fonts` is packed into the pk3 by the Dockerfile and `tools/local/build_wasm.sh`.  Only add fonts that are free to redistribute (no Microsoft fonts).

## Sample demo tests (memory!)
- `infra/web/status-api/demo` tests that read `DEMO_SAMPLES` (`TestParseSamples`, `TestSyncSamples`, `TestMeasureSample`) are skipped without it and under `-short`. The set is `.dm_NN` files extracted from the zips in `~/dev/sample_demos` (extract into a fresh dir; the zips are not read directly).
- `demo.Parse` keeps every entity of every frame (~120 entities x ~400 bytes x up to 85k frames = about 4 GB for ONE long demo). Use `ParseWith(..., Options{Entities: KeepPlayers})` (what `Sync` needs) or `KeepNone`. Never cache many parsed demos; the tests hold at most two.
- Always run them under a cap, an uncapped run once OOM-killed WSL three times: `DEMO_SAMPLES=<dir> GOMEMLIMIT=4GiB systemd-run --user --scope -p MemoryMax=6G -p MemorySwapMax=0 go test -run TestSyncSamples -count=1 .`

## Client demo pipeline (`status-api/demo`)
`demoinspect -replay f.dm_60` converts one demo, `demoinspect -merge a.dm_60 b.dm_60 ...` merges the POVs of one game. Both print what `replay.Analyze` makes of the result. What a client demo does and does not carry, learned against the OSP scoreboards inside the demos (the `sc` commands: Kll/Dth/Sui/TK/DG/DR/TD per player; each round's board is cumulative for the map):
- **Kills** are `EV_OBITUARY` temp entities (victim `otherEntityNum`, killer `otherEntityNum2`, means of death `eventParm`). Entity-event numbers differ per protocol (dm_60 eType 125, dm_59 123, dm_57 121), so `detectObituary` finds the type from the data. Only count them while OSP's `gamestate` (in `cs[36]`) is 0; warm-up deaths are not scored. The same obituary seen by two demos is merged within 500 ms (same killer and victim). About 1-2% of kills still exceed the scoreboard; the ones examined were lieutenant artillery kills the scoreboard does not credit.
- **The playerState belongs to `ps.clientNum`**: the recorder while alive, but the player they follow after dying. It gives exact health at full rate. The recorder's own entity is not in the snapshot. Never stamp it on the recorder's slot.
- **`tinfo`** (about 1/s, same format in dm_57/59/60): `tinfo ident identHealth n` then client, location, health, powerups, class per teammate. Health and class of a recorder's whole team. Class is 0 soldier, 1 medic, 2 engineer, 3 lieutenant (checked by `TestClassNumbering`: artillery/airstrike killers are all class 3, rocket and sniper killers class 0). Means-of-death numbers in the demos are the mod's `meansOfDeath_t` (15-18 pistols/SMGs, 21 sniper, 46 airstrike, 49 artillery, 56 suicide).
- **Teams** change during a demo (sides swap at the round restart); use `teamTimeline`, not the final roster. `entityState.teamNum` is not the team.
- **Damage received** = health lost down to zero. Counting overkill or shots at a downed player overshoots the scoreboard's DR by 10-40%.
- **Damage given**: bullet hits (`EV_BULLET_HIT_FLESH`: victim `eventParm`, shooter `otherEntityNum`; also detected from the data) and missiles (thrower = player within 150 units of the launch point at launch time, using merged positions). Dynamite, airstrikes, artillery, fire and melee are not attributed yet.
- Tests: `TestKillEventsVsScoreboard`, `TestDamageReceivedVsScoreboard`, `TestDamageGivenVsScoreboard`, `TestMergeSamples`; run them under the memory cap above.

## Client-demo replays (side project: upload old `.dm_xx` demos, watch them in 3D)
The plan is `~/.claude/plans/in-s4ndmod26-we-have-calm-breeze.md` (hard constraints: unlisted, own storage, no `.rpl` synthesis).
- Pages: `infra/web/nginx/html/demos/index.html` (upload, per-demo verdict, coverage per game) and `demos/view.html` (a fork of `paths.html` fed by `/api/userdemos/{id}/paths?group=N`; no nav, no analytics, `noindex`). Nothing links to them.
- API: `status-api/userdemos.go`. Off unless `USERDEMO_UPLOAD_ENABLED=1` **and** `USERDEMO_UPLOAD_KEY` is set (then the routes do not exist). Also `USERDEMO_DIR` (default `/userdemos`, mounted from `gamedata/userdemos`, git-ignored, never under the served downloads), `USERDEMO_MAX_FILE_MB` (40), `USERDEMO_MAX_DEMOS` (40), `USERDEMO_MAX_AGE_DAYS` (7), `USERDEMO_SESSIONS_PER_HOUR` (5), `USERDEMO_UPLOADS_PER_HOUR` (60). Ids are 128 random bits, no listing endpoint; uploads and removal need `X-Upload-Key`, viewing needs only the id. nginx sets `X-Real-IP` for the rate limits (`/api/userdemos` location, which also raises the body limit to 40 MB).
- One parse/merge job at a time; an upload is compared with up to 3 demos of each existing group, read back from disk, so at most 2 demos are parsed at once. `demo.MergeStream` loads demos one at a time for the same reason.
- Run it locally without Docker: build `status-api`, start it with `STATUS_API_PORT=18080 REPLAY_DIR=<empty dir> USERDEMO_UPLOAD_ENABLED=1 USERDEMO_UPLOAD_KEY=k USERDEMO_DIR=<dir>` under the memory cap, and serve `infra/web/nginx/html` with `/api` proxied to it (nginx does this in production). Without the game's pk3 the map mesh 404s and the viewer shows a grid.
- Tests: `TestUserDemos*` in `status-api` (the guards always, `TestUserDemosFlow` with `DEMO_SAMPLES`).

## Gotchas
- **Line endings:** several mod files are CRLF (some mixed), e.g. `cg_weapons.c`, `cg_local.h`.  Edit them with the Edit tool or byte-level scripts; a Python text-mode read/write rewrites every line ending and buries the change in a whole-file diff.  Check `git diff --stat` after editing.
- `node` is not on PATH in the dev shell; use the emsdk copy: `export PATH=$HOME/.local/share/emsdk/node/24.19.0_64bit/bin:$PATH`.
- Headless swiftshader screenshots take ~1.3 s each and lag the game; to catch a fast event (a rocket flight is ~0.3 s) set `g_replayDebug 1`, watch the console for `frame N serverTime T`, and set `timescale 0.1-0.3` (`Module.ccall('wasm_set_cvar',null,['string','string'],['timescale','0.2'])`) when T nears the event.  Temporary `CG_Printf` prints in cgame show up in the page console.
- Missile explosions are not in archives: `G_ExplodeMissile` keeps the blast in the entity event ring and frees it in 300 ms, so playback rebuilds blasts when a missile stops being recorded (`G_ReplayExplodeVanished`).
- Replay entities are forced `inuse` over map slots; temp entities (`G_TempEntity`) are how playback fires one-shot effects (obituaries, blood, blasts).
- Commit messages/PRs: follow the attribution lines in the session instructions.
