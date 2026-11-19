# Plan: full-game in-engine playback (free fly, player switching, scrubbing), with merged client demos as a second source

Status: planning notes (not committed). Written 2026-10-31. Companion to `PLAN_osp_stats.md` (goal 4, the full replay viewer).

Labels used below: **[mod]** = verified in this repo or stated by the owner, **[inferred]** = deduced, not confirmed.

## Goal
Watch back an entire game inside the engine with modern demo-viewer controls:
- free-fly camera, and switching between players during playback
- timeline scrolling and scrubbing
- play, pause, slow-mo and fast-forward buttons

Optionally roll in the merged client demos from the web demo-replay tool (`infra/web/status-api/demo*`, PR #84) so that games
nobody recorded server-side can be watched the same way.

## Where things stand
- **[mod]** The mod replays only highlights and play of the game today. The whole engine, client and server, loads in the
  wasm client and plays back chosen portions from one player's perspective.
- **[mod]** Server-side demos are already being recorded.
- **[mod]** The web demo tool turns client demos (`.dm_57/.dm_59/.dm_60`) into merged, line-it-up-on-the-server-clock
  replays shown in a browser. That output has the same shape as the status-api `replay.Replay` (header, frames, events).

## Why not play a client demo directly
A client demo is one player's delta-compressed snapshot stream, filtered to what that player could see.
- The stock `demo` command gives a true first-person view, and `timescale` gives slow-mo and fast-forward.
- It cannot seek (a backwards scrub means restarting and fast-forwarding, or building keyframes), and it cannot switch players.
- A merged result has no single point of view, so it cannot be written back as a `.dm_59` at all.

## Chosen approach: drive the local server from per-tick state
Full snapshots of every player (position, angles, health, weapon, stance, firing) plus events. The local server puppets the players;
the client follows one or flies free.
- Seeking is random access, so scrub, pause, slow-mo and fast-forward are just moving the playhead.
- Player switching is changing who the camera follows. Free fly is a detached camera.
- Record on the server (it sees everything) and add a full-state keyframe every few seconds so a seek never replays from the start.
- Non-player state (projectiles, dynamite, mounted guns, doors and movers, ragdolls) has to be recorded and puppeted too, or it will be
  missing from playback. Decide per entity type what is worth it.

## Design: one player, two sources
```
server recording ─────────────┐
                               ├─> neutral timeline ──> in-engine player ──> controls
merged client demos ─exporter─┘      (players, events,      (puppets +        (fly, follow,
 (per-sample confidence)              round markers)         camera)           scrub, speed)
```
- The in-engine player and the controls only talk to the neutral timeline.
- Server recordings load at full fidelity.
- Merged demos are converted by an exporter into the same timeline, with a per-sample **confidence** flag (seen vs. interpolated).
  Native recordings are all "seen".

## What merged demos add or lack
- **Gaps.** Where no recorder saw a player, the merge interpolates. The player should show those stretches differently (fade, freeze
  or mark) rather than as fact.
- **State fidelity.** The web viewer needs position and aim only. Puppeting also wants stance, weapon, firing and animation state.
  [inferred] some of this is in the demo data, but what the merge keeps still has to be checked.
- **Other entities.** Projectiles and objectives are only what somebody saw: best-effort.
- **Clock.** The web viewer cuts the time between rounds. In-engine, keep real server time and put round boundaries on the timeline
  as markers so scrubbing matches what players remember.
- Demos without OSP round info stay one match, with no game or round markers.

## Suggested order
1. Define the neutral timeline format from what the server recordings already hold (read the recording format first: it decides
   what "neutral" must cover).
2. Build the in-engine player on it: pause, slow-mo, scrub, follow and free fly. Test on native recordings only.
3. Write the merged-demo exporter into the same format, with the confidence flag.
4. Polish: round markers, event feed on the timeline, kill-cam style jumps, per-player trail overlays as in the web viewer.

## Open questions
- Which format does the exporter write: a neutral format only, or also `.rpl`? The demo side project's approved plan forbids `.rpl`
  synthesis, so this needs an explicit decision when step 3 starts.
- How long are full recordings kept, and at what tick rate? (`PLAN_osp_stats.md` says full replays live 24-48h.) Old merged games
  would be a way to keep a game watchable after that, but only at merged-demo fidelity.
- What does the current highlight playback already do (server-side puppets, or a client-side demo)? If it already puppets one
  player's view, "the whole game" is mostly storage and keyframes, not new playback code.
- Can the engine show a puppet's real first-person view (weapon model, HUD), or only a camera at the puppet's eyes?
- Where does the web demo viewer (`nginx/html/demos/view.html`) fit afterwards? It is a good reference for control behaviour,
  and could read the same neutral timeline.

## References
- Web demo tool: PR #84, `infra/web/status-api/demo/` (parser, MergeDemos, rounds, clip, stats), `infra/web/status-api/demomatches.go`.
- `docs/PLAN_osp_stats.md` for the stats pipeline and the replay retention decisions.
