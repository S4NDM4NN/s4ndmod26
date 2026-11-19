# Notes: free-cam replay viewer, automated gamecast and live stream

Status: ideas and findings only, nothing built (written 2026-11-17, from a design discussion).  Companion to
`PLAN_fullgame_playback.md` (which covers the same ground for the web viewer and merged client demos; read it first).

Labels: **[verified]** = read in this repo, **[inferred]** = deduced, not tested.

## Goals
1. **Free-cam viewer** (browser): watch a whole recorded match with demo controls like a Fortnite or Overwatch replay: play, pause,
   slow-mo, fast-forward, scrub, follow any player (first person or chase), free-fly camera.
2. **Gamecast mode**: an automated director that replays a whole match, jumps between fights (skipping dead time), and for each fight
   shows the combatants' first-person feeds side by side.  A feed drops out when its player dies (a kill is a known timestamp), with a
   transition (static, tear off).
3. **Live stream** of games (Twitch or similar), eventually, from the same director.

## Decision: what runs where
- **Viewers watch in the browser** (wasm client, as the play page does for the play of the game).  Controls there can be an HTML overlay.
- **The stream runs the native client** (iortcw, 64-bit), headless on a GPU machine, captured by ffmpeg (NVENC) to RTMP, audio from a
  PulseAudio null sink.  The dev cluster has NVIDIA device-plugin and dcgm-exporter namespaces [verified they exist], but whether a GPU
  is free for this is not checked.
- Native playback for players (launcher or a `s4ndmod26://replay/<name>` link starting the native client with the replay arguments)
  is wanted eventually; the same replay-server code path would serve it.
- Why native for the stream: no wasm heap ceiling for a whole match, real GPU for several scene renders per frame, simpler capture than a
  headless browser, quick local iteration (native client + native qagame, gdb) compared to `build_wasm.sh` [inferred].
- Both clients run the **same game module and cgame** (replay-server mode in `g_replay.c`), so the playback logic is built once.

### One command interface
Build the controls as game commands/cvars (seek, speed, follow N, camera mode free/follow/auto, layout and feed list), not as UI.
The browser overlay, a later in-engine UI and the stream controller all drive the same commands.  The director is a Go step that turns
the analysis into a timeline of those commands.  The stream needs no on-screen UI at all.

## What exists already
- **[verified]** `.rpl` v8: header + 5 s zlib chunks, each frame (50 ms) holds the full state of every player and of the captured entities,
  no deltas.  Any frame is usable alone, so seeking is random access in the data.  The "keyframe" need in `PLAN_fullgame_playback.md` is
  about state that is not in the frames (below).
- **[verified]** The play page already shows a followed player's own first-person view (weapon, HUD) for the clip's target, and the
  airstrike replay draws a second scene as an inset, so cgame can render a second view.
- **[verified]** The analysis has engagements: start/end, participants (damage dealt/taken, health lines), hits, kills, revives
  (`replay/analysis.go`, `Engagement`), and `/api/games/{match}/fights` (`replayviews`).  That is the director's raw material.
- **[verified]** Join/rename events, and (PR #152) `REPLAY_EVENT_MAP_BROKEN` events, are in the chunk stream with timestamps.
- **[verified]** `AGENT_REPLAY_MAX_AGE_DAYS` defaults to 30 days, so full recordings are around for a month.
- Clip files (PRs #150, #151, #152): the agent/site cut `<game>.potg.rpl` / `<game>.hl<slot>.rpl` on request, and a slice carries
  an events-only chunk of the names and destroyed entities from before it.  The same idea, carrying world state ahead of a start point,
  generalises to seek state (below).

## What is missing
1. **Seekable playback engine.**  The loader reads the file once, in order, and the game's file API has no seek (`G_ReplayLoadFromFile`
   read-and-discards).  A whole match is roughly 17 MB compressed for 15 minutes [verified for one sample: 17,997 frames, 900 s] and
   about ten times that unpacked [inferred]: too much to hold casually in wasm.  Options: stream chunks around the playhead (the file is
   already chunked, and agent/nginx serve Range), and/or add a seek syscall to our engine (bumps `EngineAPI`, only our engine builds
   would have it; replay playback never ran on the original 32-bit engine, so that is fine).
2. **World state at the playhead.**  Rebuilding the world on a jump:
   - players, missiles, items, corpses, movers away from home, traps: from the frame samples;
   - names: join/rename events (the loader ignores LEAVE and applies only events it loaded);
   - destroyed breakables: `MAP_BROKEN` events (old recordings: the sidecar's `broken_` lines).  **Playback only removes entities** (`G_ReplayApplyBroken`),
     so seeking backwards needs them put back;
   - objectives (flags, dynamite plants, spawn captures): events, and which of those are entities in the frames [inferred, not checked];
   - scoreboard and stats at a moment: derived from events [inferred].
   A periodic **state chunk** (names, broken set, objective state) would let a seek start from the nearest one, the same trick as the clip
   slice's carried events.
3. **Free cam and demo controls.**  Free fly via the stock spectator movement in the replay server [inferred, not tried there], follow
   any player (build that player's playerState from their samples; the POTG already does it for one target), scrub/speed/pause.
4. **Director and multi-feed.**  Per-feed fake playerState and one scene per tile; 2-4 tiles is realistic in a browser, more natively
   [inferred].  Transitions on death.  Skipping between fights is a seek to the next engagement start minus padding.
5. **Stream plumbing.**  Headless native client on a GPU box, ffmpeg capture, audio, a controller that sends the director's commands,
   overlays (scoreboard, kill feed, fight labels).  A live game: feed the replay path from the growing recording with a short delay
   (the site's live view already does that with `LIVE_VIEW_DELAY_SECONDS`).  **Not** by joining as a spectator: a spectator only receives
   what its camera can see, so several first-person feeds would not be possible [inferred].

## Suggested order
1. **Native spike**: load a full recording in the native client and seek around; settle state rebuilding (including un-breaking walls)
   without wasm limits.
2. Command interface, plus a minimal controller script, running locally.
3. Director (Go, from engagements) and tiles, with death transitions.
4. GPU machine capture to RTMP.
5. Browser overlay on the same commands; chunk streaming for wasm; native playback for players via the launcher.

## Risks
- Wasm memory for a whole match (streaming needed there).
- Backward-seek correctness: dynamite, flags, corpses, breakables.
- GPU cost of several scenes per frame in a browser.
- Entities that are not recorded show nothing; 20 Hz sampling needs smoothing (the client already interpolates entities).
- Sound sync for the stream.

## Open questions
- Is a GPU free for the stream box, and where does it run?
- Which comes first: the free-cam viewer for players, or the broadcast cast?
- Seek via a new engine syscall, or chunk streaming through the existing API only?
- Does the director write its own timeline file kept next to the analysis, or is it computed at play time?
- Audio for the stream: game sound only, or a commentary track later?
- Merged client demos as a second source: see `PLAN_fullgame_playback.md` (they forbid `.rpl` synthesis, so a neutral timeline is needed).
