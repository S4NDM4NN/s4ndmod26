# WASM Troubleshoot Notes - Codex

## Summary

### 2026-06-27 update

The older summary below is now partially stale.

Current state is better than "persistent black screen":
- a diagnostic WASM build that skips `NET_Init()` does reach first frame, first `UI_REFRESH`, and menu audio/input.
- the HTML shell sizing/visibility issues were fixed; the remaining defect is inside the game-rendered UI.
- current visible symptom is "menu renders, but text/UI composition is wrong", not "nothing reaches the browser".

Most important current conclusions:
- startup hang root cause is still the proxied WASM socket path, specifically the first `socket()` call in `NET_IPSocket()`.
- once that path is bypassed, the next issue is renderer/UI correctness.
- font assets are loading correctly; the remaining UI problem is likely in the 2D renderer color/modulation path rather than missing assets or bad menu scripts.

Fast iteration is also now in place:
- `make wasm-build`
- `make wasm-deploy`
- `make wasm-iterate`

These use the cached Docker WASM path and currently rebuild+deploy in about a minute instead of requiring a full web image rebuild.

Current browser symptom is no longer the earlier `invoke_vi` crash. The game now appears to start, switches to the canvas, but the browser shows a persistent black screen.

The earlier duplicate-side-module-load root cause still looks valid historically, but the currently running web build appears to already include that fix:
- `iortcw/Makefile` no longer links `cgame.mp.wasm` and `ui.mp.wasm` into `CLIENT_LDFLAGS`.
- The live `index.wasm` in the running `web` container does not contain `neededDynlibs`, `cgame.mp.wasm`, or `ui.mp.wasm` strings.

That makes the black-screen issue likely a second problem, probably in renderer state or side-module symbol resolution rather than startup asset loading.

After the multitexture disable patch was deployed, Chrome DevTools confirmed the live build now reports:
- `...not using GL_ARB_multitexture in WASM (missing client-state support)`
- `GL_MAX_TEXTURE_UNITS_ARB: 1`
- `multitexture: disabled`

So the current black-screen/hang is not explained by the original half-enabled multitexture state alone.

## Findings

### 0. Current UI-render state after bypassing networking

With `NET_Init()` skipped under `__EMSCRIPTEN__`, the browser client now:
- reaches `Com_Frame`
- reaches `SCR_UpdateScreen`
- reaches `UI_IS_FULLSCREEN`
- reaches `UI_REFRESH`
- plays menu music
- responds to mouse input

Observed menu symptom:
- large art assets render
- logo renders
- some panels render as opaque/incorrect black regions
- menu text is missing or visually wrong

Interpretation:
- this is no longer a generic "game is hung" symptom.
- enough of the renderer works to draw textured UI art.
- the remaining fault is likely a narrower fixed-function renderer state issue affecting text and color-modulated 2D quads.

### 0.1. Shell/frontend presentation bugs were real, but they are no longer the main issue

`wasm/shell.html` was adjusted so the browser shell no longer fights the canvas:
- `spinner`, `status`, and `progress` are hidden in `hideConsole()`
- `body` uses `min-height: 100vh` and `overflow: hidden`
- canvas sizing preserves a centered 4:3 play area

After deploy, browser inspection confirmed:
- hidden shell status elements
- body overflow disabled
- canvas sized to viewport

This means the remaining ugliness is coming from the RTCW frame itself, not from HTML/CSS chrome.

### 0.2. Font assets are loading; text draw calls are happening

Added one-shot diagnostics in:
- `iortcw/code/renderer/tr_font.c`
- `mod/src/ui/ui_main.c`

Live browser logs confirmed:
- `fontImage_16.dat` loads successfully
- glyph lookups for menu text are valid
- glyph shader names are valid
- `Text_Paint("SINGLE PLAYER", ...)` is executing

Representative values observed:
- glyph shader `fonts/fontImage_0_16.tga`
- glyph image size `8x11`
- scale about `0.765`
- glyphScale `3.000`

Interpretation:
- missing text is not explained by absent font files.
- menu scripts are not the primary issue.
- the break is downstream, in how the renderer draws the glyph quads.

### 0.3. Current leading renderer hypothesis: broken 2D color-array / modulation path in WASM

Why this is now the top UI hypothesis:
- baked textured art still renders
- font glyphs are valid and text draw calls execute
- menu scripts use visible colors
- many incorrect areas look like bad color/blend/modulation rather than bad texture lookup

Renderer path of interest:
- `RE_SetColor()` queues UI RGBA
- `RB_SetColor()` writes `backEnd.color2D`
- `RB_StretchPic()` copies `backEnd.color2D` into `tess.vertexColors`
- `RB_StageIteratorGeneric()` / `RB_IterateStagesGeneric()` feed the color array to GL

Current diagnostic patch:
- in `iortcw/code/renderer/tr_shade.c`, under `__EMSCRIPTEN__` and only for `backEnd.projection2D`, force constant GL color with `qglColor4ubv(backEnd.color2D)` and disable `GL_COLOR_ARRAY`
- goal: determine whether the remaining UI corruption is specifically caused by the WASM color-array path

Build/deploy status:
- patch compiled successfully
- rebuilt and deployed with `make wasm-iterate`

Testing status:
- interactive Chrome DevTools MCP lost page-selection state and could not be used directly
- raw CDP websocket access to the existing Chrome session was rejected by Chrome origin allowlisting (`403 Forbidden` from the DevTools websocket)
- a fallback headless Chromium run against `http://127.0.0.1:99/play/` only reached PK3 download progress within the time budget, so it did not validate the warmed in-browser menu state

Result:
- the new renderer diagnostic build is live
- visual confirmation of whether the constant-color probe fixes the menu still needs either:
  - the user's warm browser session and a hard refresh, or
  - Chrome restarted with `--remote-allow-origins=*` so CDP inspection can attach cleanly

### 0.4. Important build-pipeline bug found during WASM iteration

The fast Docker path was not always shipping fresh native gamecode changes.

Root cause:
- after rebuilding `main/cgame.mp.wasm` and `main/ui.mp.wasm`, the second Emscripten pass sometimes considered `build/release-emscripten-wasm/index.html` and `index.data` up to date
- that meant UI-side diagnostic changes could compile, but the browser still received an older preloaded bundle

Fix applied:
- in `Dockerfile`, after copying fresh side modules into `wasm/fs/main/`, force-delete:
  - `build/release-emscripten-wasm/index.html`
  - `index.data`
  - `index.js`
  - `index.wasm`
  - `index.worker.js`
  - `build/release-emscripten-wasm.zip`
- then rerun the client build pass

Impact:
- fresh `ui.mp.wasm` / `cgame.mp.wasm` diagnostics now reliably reach the browser
- this was confirmed because later browser console output changed to newer UI diagnostics that had previously been missing

### 0.5. Font-texture hypothesis has now been weakened

Two targeted probes were tested in Chrome against fresh rebuilt pages:

1. Force constant white only for `fonts/*` shaders in `tr_shade.c`
2. Replace font glyph texture draws in `Text_PaintChar()` with solid white `whiteShader` quads

Observed result:
- neither probe made the missing menu labels appear in the screenshot
- the second probe did reach the browser (confirmed by console log), so this is not a stale-bundle artifact

Interpretation:
- the problem is no longer best explained as "font texture missing" or "font alpha/color modulation only"
- the text quads are either:
  - being drawn to the wrong screen region,
  - being clipped, or
  - being covered by later UI passes

### 0.6. First missing menu glyph is being submitted, but far into the lower black region

Latest browser console from the solid-glyph probe logged:
- first `Text_Paint("SINGLE PLAYER", ...)` executes
- first solid glyph quad lands at approximately:
  - `x=19.20`
  - `y=641.31`
  - `w=9.79`
  - `h=13.46`
  - on a `1024x768` GL mode

What matters:
- the screenshot's visible menu art occupies the upper portion of the canvas, while the lower region is mostly black
- the first menu text glyph is being submitted deep in that lower black region
- a crop of that lower-left area still did not show the expected white glyph blocks

Current interpretation:
- the UI text path is active, but the visual issue is now most consistent with a 2D layout / clipping / overdraw problem
- this is stronger than the earlier pure-font hypothesis

Likely next investigation target:
- 2D UI placement / covering passes after `Text_PaintChar()`
- especially full-screen or large black `DrawStretchPic` / `FillRect` draws that may be overwriting the lower menu region

### 0.7. 2026-06-29 live menu capture narrowed the bug past "browser scaling"

Current live browser measurements from DevTools:
- canvas backing store: `640x480`
- browser CSS presentation: approximately `1106x830`
- device pixel ratio: `1.25`

What this rules out:
- the browser is doing a normal 4:3 upscale of a `640x480` render target
- the current menu corruption is not explained by the page stretching the canvas to a random aspect ratio

Menu composition was also rechecked against `mod/main/ui_mp/main.menu`:
- large shader quads:
  - `bands` at `200,-480 640x1024`
  - `backimage4` at `0,0 343x480`
- model items:
  - `testmodel_nflag1`
  - `testmodel_icon_burn`
- later 2D overlay items:
  - `BLACKGRAD`
  - `BLACKGRAD2`
  - `FLAME`
  - `WOLFFLAMELOGO`
  - `WOLFICON`
  - `gold_line`

Live WASM logs still show the 2D overlay submission path receiving the expected virtual-space rects:
- `WOLFFLAMELOGO rect=200,-5 240x120`
- `WOLFICON rect=220,380 200x100`
- `gold_line rect=0,410 250x10`, `390,410 250x10`, `0,76 226x10`, `414,76 230x10`

But the live screenshot still shows:
- the top header/logo landing around the middle of the frame
- the bottom wolf icon also landing too high
- large opaque black regions occupying areas that should only be gradients/bands

Current interpretation:
- the bug is now more consistent with later menu passes being clipped or transformed after intermediate menu model renders
- this is stronger than the earlier "font-only" or "whole-canvas scaling" hypotheses

### 0.8. Explicit 2D-state restore after UI model scenes did not fix the frame

Experiment:
- patched `iortcw/code/renderer/tr_backend.c`
- after `RB_RenderDrawSurfList()` in `RB_DrawSurfs()`, if `cmd->refdef.rdflags & RDF_NOWORLDMODEL`, explicitly call `RB_SetGL2D()`
- goal: force a clean full-screen 2D viewport/scissor/projection reset immediately after menu-model subviews

Reason for the experiment:
- the menu order places `testmodel_nflag1` and `testmodel_icon_burn` before several corrupted 2D items
- the screenshot pattern looked compatible with a leaked sub-viewport or scissor box from those model renders

Observed result after `make wasm-build`, `make wasm-deploy`, browser reload, and fresh screenshot:
- no visible improvement
- the header/logo and bottom icon still render in the wrong vertical region
- the same oversized black regions remain

Interpretation:
- if the corruption is related to model subviews, it is deeper than a simple missed `RB_SetGL2D()` call at the backend command boundary
- alternate possibilities now include:
  - gl4es/WebGL mishandling viewport or scissor restoration even when RTCW asks for a reset
  - one or more large menu shader quads (`bands`, `backimage4`, `BLACKGRAD`, `FLAME`) being uploaded or sampled incorrectly
  - a transform/clipping discrepancy between the engine's virtual `640x480` layout and how the WASM renderer applies those coordinates during later passes

Open note:
- one diagnostic log currently prints the main-menu model handle as `model=0`
- that needs re-validation, because `Item_Model_Paint()` normally returns early when `item->asset` is null
- so that specific print may be misleading or may reflect a separate data-path oddity

### 0.9. Next active trace: verify menu-model viewport/scissor leakage directly

Current hypothesis under test:
- the main-menu 3D model renders may be using a larger or shifted viewport/scissor than intended
- or gl4es/WebGL may be failing to restore that state cleanly before later 2D menu quads are drawn

Instrumentation added on 2026-06-29:
- `iortcw/code/renderer/tr_scene.c`
  - log every `RDF_NOWORLDMODEL` `RE_RenderScene()` call with:
    - `refdef x/y/width/height`
    - `fov_x/fov_y`
    - `glConfig.vidWidth/vidHeight`
    - computed backend viewport
- `iortcw/code/renderer/tr_cmds.c`
  - expand tracked `RE_StretchPic()` logging to include:
    - `bands`
    - `backimage4`
    - `BLACKGRAD`
    - `FLAME`
    - `WOLFFLAMELOGO`
    - `WOLFICON`
    - `gold_line`
- `iortcw/code/renderer/tr_backend.c`
  - expand main-decor backend logging to the same shader set
  - log:
    - `backEnd.viewParms.viewport*`
    - live `GL_VIEWPORT`
    - live `GL_SCISSOR_BOX`
    - whether the pass is in `projection2D`

Build/deploy status:
- compiled successfully
- deployed successfully with `make wasm-build` and `make wasm-deploy`

Current blocker:
- Chrome DevTools MCP intermittently loses the selected target even while the `/play/` tab remains alive in the browser
- until the tool reattaches cleanly, the new logs are not yet captured in this note

### 0.10. Confirmed: later 2D menu quads inherit the menu-model viewport/scissor

Live DevTools capture on 2026-06-29 confirmed the following:

Menu model `RenderScene()` viewports:
- `testmodel_nflag1`
  - `refdef=-349,-29 798x598`
  - computed viewport `-349,-89 798x598`
- `testmodel_icon_burn`
  - `refdef=141,267 358x324`
  - computed viewport `141,-111 358x324`

These are both outside the normal `640x480` render target bounds:
- negative `viewportY`
- one viewport significantly wider/taller than the target itself

Most important confirmation:
- the next 2D menu quads do **not** render with a restored full-screen 2D viewport
- backend logs for `bands`, `backimage4`, `BLACKGRAD`, `FLAME`, `WOLFFLAMELOGO`, `WOLFICON`, and `gold_line` show:
  - `proj2D=1`
  - but `backEndView`, `GL_VIEWPORT`, and `GL_SCISSOR_BOX` still equal the previous model-scene viewport

Representative evidence:
- after `testmodel_nflag1`:
  - `WASM decor quad: shader=BLACKGRAD ... glViewport=-349,-89 798x598 glScissor=-349,-89 798x598`
- after `testmodel_icon_burn`:
  - `WASM decor quad: shader=WOLFFLAMELOGO ... glViewport=141,-111 358x324 glScissor=141,-111 358x324`
  - same leaked viewport/scissor persists for `WOLFICON` and all `gold_line` quads

Interpretation:
- this is the first direct proof that the broken menu composition is not just an abstract scaling issue
- later 2D UI passes are being rasterized inside leaked model-scene viewport/scissor boxes
- that explains why the top logo and bottom icon land in the wrong vertical region even though their submitted `640x480` virtual coordinates are correct

Important follow-up implication:
- calling `RB_SetGL2D()` in `RB_DrawSurfs()` after `RDF_NOWORLDMODEL` scenes was not enough under this WASM/gl4es path
- either:
  - gl4es is not honoring the later viewport/scissor reset as expected, or
  - the 2D path is reusing `backEnd.viewParms` / GL state in a way that immediately reapplies the model-scene viewport

Next troubleshooting step:
- patch the 2D draw path itself so that `RB_StretchPic()` and related 2D entry points forcibly restore full-screen viewport/scissor when `projection2D` begins, independent of the previous `backEnd.viewParms`
- if that fixes the frame, the bug is effectively a state-leak / restore bug between NOWORLDMODEL scenes and later UI 2D passes

### 0.11. Forcing `RB_SetGL2D()` for every `RB_StretchPic()` did not visually fix the menu

Diagnostic patch:
- in `iortcw/code/renderer/tr_backend.c`, under `__EMSCRIPTEN__`, call `RB_SetGL2D()` on every `RB_StretchPic()` even when `backEnd.projection2D` is already true
- intent: override any stale viewport/scissor leakage before each 2D menu quad draw

Observed result after rebuild/deploy/reload:
- no visible improvement in the live menu screenshot
- the top logo still renders around the middle of the frame
- the lower wolf icon remains too high
- large clipped/black regions still remain

Interpretation:
- the problem is not solved by simply reissuing the standard 2D setup block before each stretch-pic
- this suggests one of the following:
  - gl4es is caching or reapplying the stale viewport/scissor after RTCW asks for the reset
  - the actual bad state lives outside the specific `RB_SetGL2D()` calls we are reissuing
  - another transform/clipping path is involved beyond plain viewport/scissor restoration

Implication for next step:
- the investigation should move deeper than `RB_SetGL2D()` itself
- likely candidates now are:
  - explicit low-level `qglViewport`/`qglScissor` enforcement closer to the final draw call
  - gl4es state caching behavior
  - or a path where `backEnd.viewParms` / matrix state is being reused in a way the current reset does not fully replace

### 1. The frame pump is probably alive

Evidence:
- `sys_main.c` uses `emscripten_set_main_loop(Com_Frame, 0, 0)` in WASM.
- `common.c` now skips `NET_Sleep()` under `__EMSCRIPTEN__`, which should avoid a browser main-thread stall caused by `Atomics.wait`.
- `GLimp_EndFrame()` still calls `SDL_GL_SwapWindow()` in WASM.
- Nginx/web logs show clean delivery of `/play/index.js`, `/play/index.data`, and `/play/index.wasm` with no server-side errors.

Interpretation:
- This does not look like a simple "main loop never advances" failure.
- A black screen is more likely "frames are being swapped but nothing valid is rendered".

### 2. Leading renderer hypothesis: broken fixed-function multitexture state in WASM/gl4es

Most likely current root cause.

Relevant code:
- `iortcw/code/sdl/sdl_glimp.c`
- `iortcw/code/renderer/tr_backend.c`
- `iortcw/code/renderer/tr_shade.c`

What changed:
- The WASM-specific `GLimp_InitExtensions()` path enables multitexture by setting `qglActiveTextureARB = SDL_GL_GetProcAddress("glActiveTexture")`.
- That same path explicitly leaves:
  - `qglClientActiveTextureARB = NULL`
  - `qglMultiTexCoord2fARB = NULL`

Why that is dangerous:
- The fixed-function renderer still behaves as if multitexture is available.
- `GL_SelectTexture()` changes the active texture unit, but only changes the client texture unit if `qglClientActiveTextureARB` exists.
- `DrawMultitextured()` sets texture coordinates for unit 0, switches to unit 1, then sets texture coordinates again.
- If client texture unit switching is unavailable, the second `qglTexCoordPointer()` likely updates the wrong client array.
- Result: the renderer may bind textures and issue draws, but the texture coordinate state is invalid for multitexture passes.

Why this matches the symptom:
- World/UI passes could render as black or effectively invisible while the app continues running and swapping frames.
- This is a better fit for "black canvas with no obvious hard crash" than the old dynamic-linking failure.

### 3. Secondary hypothesis: `ui` may still capture wrong function pointers from `cgame`

This is still plausible, though it now looks secondary to the renderer issue.

Relevant code:
- `iortcw/code/client/cl_cgame.c`
- `iortcw/code/client/cl_ui.c`
- `mod/src/ui/ui_main.c`
- `mod/src/cgame/cg_syscalls.c`
- `mod/src/ui/ui_syscalls.c`

Observed behavior:
- `cgame` loads before `ui`.
- `ui` stores many function pointers into `uiDC` during `UI_INIT`.
- `cgame` and `ui` export many same-named trap wrappers, but those wrappers map to different syscall enum values.
- Generated Emscripten runtime logic in `index.js.debug` prefers already-merged global symbols and only fills GOT function slots once.

Possible effect:
- `ui` may capture `cgame` wrappers for shared symbols like `trap_R_DrawStretchPic`, `trap_R_RegisterModel`, or `trap_S_RegisterSound`.
- That would route UI calls through the wrong syscall numbers.

Why this is not the top suspect for the black screen:
- The symptom sounds more like rendering/state failure than immediate UI logic corruption.
- The renderer multitexture mismatch has a more direct path to "all black but still running".

### 4. New live-browser stall point: after `NET_Init()`

Chrome DevTools on `http://127.0.0.1:99/play/` now shows a clean startup log through:
- `Sys_LoadGameDll(/main/ui.mp.wasm) found vmMain function ...`
- `--- Common Initialization Complete ---`
- `Opening IP socket: 0.0.0.0:27960`

What did not appear:
- any later `CL_InitCGame` log
- any explicit first-frame/UI logs
- the old `shell.html` canvas pixel debug log

Interpretation:
- The runtime gets through filesystem init, renderer init, sound init, UI DLL load, and `NET_Init()`.
- The remaining wedge is most likely in the browser handoff or first-frame path:
  - `wasm_hide_console()`
  - `emscripten_set_main_loop(Com_Frame, 0, 0)`
  - first `Com_Frame`
  - first `SCR_UpdateScreen`
  - first `VM_Call(uivm, UI_IS_FULLSCREEN/UI_REFRESH)`

Follow-up probe result:
- instrumented logs confirmed the stall is even narrower than that.
- none of the post-`NET_Init()` handoff logs fired.
- the last log remained `Opening IP socket: 0.0.0.0:27960`.

That leaves the `signal(SIGILL/SIGFPE/SIGSEGV/SIGTERM/SIGINT, ...)` block in `sys_main.c` as the last engine-side code executed before the WASM/browser handoff.

Current experiment:
- skip POSIX signal registration under `__EMSCRIPTEN__`
- rebuild and retest whether startup finally reaches:
  - `WASM handoff: hiding console`
  - `WASM handoff: registering main loop`
  - first `Com_Frame`

### 5. `shell.html` debug pixel probe was a bad diagnostic fit

`shell.html` contained a debug `setTimeout(... gl.readPixels ...)` probe inside `hideConsole()`.

Why it is risky:
- if the WebGL context is already in a bad state, `readPixels()` can itself block and make DevTools evaluation/screenshot collection look like a worse hang than the original issue.
- its expected log line never appeared in Chrome, so it was not helping narrow the root cause.

### 6. Port-`27960` hypothesis tested and narrowed

Change tested:
- In `iortcw/code/qcommon/net_ip.c`, under `__EMSCRIPTEN__`, force `NET_OpenIP()` to use `PORT_ANY` instead of the default server port `27960`.
- Also skip the normal `port + i` scan loop in WASM and perform a single IPv4 socket-open attempt.

Live result after rebuild and Chrome retest:
- the last console line changed from:
  - `Opening IP socket: 0.0.0.0:27960`
- to:
  - `Opening IP socket: 0.0.0.0:-1`
- and the runtime still wedged immediately after that line.

Interpretation:
- this rules out "trying to bind local port 27960" as the specific root cause.
- the failure is still in the same narrow area: the WASM socket-open/bind path itself, or the code that runs immediately after `NET_IPSocket()` logs but before the first post-`NET_Init()` handoff print.
- this increases suspicion that the Emscripten proxied-socket backend is incompatible with the current bridge setup, rather than the chosen port number being the main problem.

### 7. Exact hang boundary confirmed: first proxied `socket()` call

Trace build added step-by-step WASM logs inside `NET_IPSocket()` around:
- `socket()`
- `ioctl(FIONBIO)`
- `setsockopt(SO_BROADCAST)`
- `bind()`

Live browser result:
- startup reached:
  - `Opening IP socket: 0.0.0.0:-1`
  - `WASM NET_IPSocket: before socket()`
- and then stopped there.

What did **not** appear:
- `WASM NET_IPSocket: socket() ok`
- `WASM NET_IPSocket: socket() failed`
- any WebSocket request in Chrome DevTools

Interpretation:
- the runtime is wedging inside the very first proxied `socket(PF_INET, SOCK_DGRAM, IPPROTO_UDP)` call itself.
- the stall happens before `ioctl`, `setsockopt`, `bind`, and before any visible browser WebSocket traffic.

### 8. Diagnostic proof: skipping `NET_Init()` unblocks the whole frame path

Built a temporary WASM diagnostic that skips `NET_Init()` entirely in `main_init()`.

Live browser result after rebuild:
- startup reached all expected handoff/frame logs:
  - `WASM handoff: hiding console`
  - `WASM handoff: registering main loop`
  - `WASM frame: entered first Com_Frame`
  - `WASM frame: entered first CL_Frame`
  - `WASM frame: calling first SCR_UpdateScreen`
  - `WASM frame: entered first SCR_DrawScreenField`
  - `WASM frame: querying UI_IS_FULLSCREEN`
  - `WASM frame: UI_IS_FULLSCREEN returned 1`
  - `WASM frame: calling first UI_REFRESH (menu)`

Conclusion:
- the current black-screen hang is decisively caused by the WASM networking startup path.
- renderer init, main-loop registration, first frame, and first UI refresh all work when `NET_Init()` is removed from the equation.
- this moves the root-cause focus away from rendering and onto the Emscripten `PROXY_POSIX_SOCKETS` setup itself.

## Artifact Checks Performed

- Confirmed live web container serves:
  - `index.js`
  - `index.data`
  - `index.wasm`
  - `cgame.mp.wasm`
  - `ui.mp.wasm`
- Confirmed live `index.wasm` does not appear to embed side-module names in the startup dylink metadata.
- Confirmed `Com_Frame` no longer sleeps under Emscripten.
- Confirmed shell frontend switches from console to canvas immediately via `wasm_hide_console()` before entering the main loop.
- Confirmed `shell.html` has a debug pixel sampler that should log the center pixel after canvas display.

## Best Current Root-Cause Ranking

1. Emscripten proxied-socket path wedging inside the very first proxied `socket()` call.
2. Cross-module `GOT.func` / merged-symbol collision between `cgame` and `ui`.
3. Renderer fixed-function state issue beyond the already-disabled multitexture path, but no longer the primary blocker for startup.
4. Less likely: POSIX signal registration or browser handoff code.

## Next Checks To Run

Highest-value checks:

1. Inspect browser console logs during startup.
   - Specifically trace whether execution reaches:
     - `wasm_hide_console()`
     - first `Com_Frame`
     - first `SCR_UpdateScreen`
     - first `UI_IS_FULLSCREEN`
     - first `UI_REFRESH`

2. Verify renderer init messages in WASM.
   - This is now mostly complete.
   - Current live build confirms multitexture is disabled in WASM.

3. Instrument the first frame path with one-shot logs.
   - This is now the highest-value narrowing step.

4. If the first frame reaches UI VM calls, instrument `ui` and `cgame` trap wrapper resolution next.
   - Focus on shared exports used in `uiDC` setup.

## Assumptions

- The current symptom is the black-screen behavior, not the older `invoke_vi` crash.
- The running `web` container inspected during this session reflects the current browser-served build.
- `index.js.debug` is representative enough to reason about the Emscripten dynamic-linking behavior even if it is not byte-for-byte identical to the production `index.js`.

## UI Render Narrowing

- The one browser `404` seen during a good run is only `GET /favicon.ico`; it is not part of the UI/render problem.
- A live screenshot of the first successful frame shows the engine is drawing real menu content:
  - RTCW logo
  - animated/fire menu art
  - large side-flag background art
  - modal quit dialog text/buttons
- This is important because it rules out "UI never renders" as the current root cause. The UI VM is alive and producing visible draw output.
- Two browser shell issues were confirmed in DevTools:
  - `status` remains visible with text `Starting...` after `wasm_hide_console()` switches from the console to the canvas.
  - the canvas is presented as a fixed `1024x768` block, so smaller browser viewports show a cropped/scrollable page instead of a fitted game view.
- Live DOM measurements before the shell fix:
  - viewport: `756x807`
  - canvas backing store: `1024x768`
  - canvas client size: `1024x768`
  - document scroll width: `1024`
- Current interpretation:
  - the first successful frame was being obscured by leftover loader/status UI
  - some of the "looks wrong" feedback is likely browser presentation, not a renderer dead path
- I patched `wasm/shell.html` to:
  - hide `spinner`, `status`, and `progress` inside `hideConsole()`
  - set page overflow hidden
  - fit the canvas into the viewport while preserving the 4:3 aspect box
- This shell fix is intentionally isolated from the renderer and from the temporary `NET_Init()` skip diagnostic.

## 2026-06-29 Menu Viewport Investigation

### 0.10. Proven: WASM menu model scenes leak viewport/scissor state into later 2D quads

- live diagnostics showed `RDF_NOWORLDMODEL` menu model renders using sub-viewports such as:
  - `-349,-89 798x598`
  - `141,-111 358x324`
- later 2D menu decor quads (`BLACKGRAD`, `WOLFFLAMELOGO`, `WOLFICON`, `gold_line`) logged:
  - `proj2D=1`
  - `backEndView=0,0 640x480`
  - but actual `glViewport` / `glScissor` still equal to those model-scene values
- this is hard proof that the menu corruption is not just bad 2D coordinates; the GL viewport/scissor state itself is wrong for later UI passes on WASM

### 0.11. Reissuing `RB_SetGL2D()` did not visually fix the menu

- tried two variants in `iortcw/code/renderer/tr_backend.c`:
  - call `RB_SetGL2D()` after `RB_RenderDrawSurfList()` for `RDF_NOWORLDMODEL` scenes
  - call `RB_SetGL2D()` for every `RB_StretchPic()` under WASM
- neither variant changed the visible frame composition
- after additionally forcing `backEnd.viewParms.viewport* = 0,0,640,480` in `RB_SetGL2D()`, logs still showed stale model-scene `glViewport` / `glScissor`
- current interpretation:
  - RTCW bookkeeping can be reset
  - but gl4es/WebGL is still retaining or reapplying the bad viewport/scissor state below that layer

### 0.12. Current experiment: clamp `RDF_NOWORLDMODEL` scene viewports before they reach GL

- rationale:
  - the menu model scenes are the first place we can prove negative / oversized viewport values exist
  - if gl4es/WebGL does not recover cleanly after those values, the next best diagnostic is to prevent them entirely on WASM
- code change:
  - in `iortcw/code/renderer/tr_scene.c`, under `__EMSCRIPTEN__` and `RDF_NOWORLDMODEL`, clamp `parms.viewportX/Y/Width/Height` to the canvas backing store bounds
  - keep logs for both original and clamped viewport values
- success criteria:
  - if the menu composition improves, the issue is effectively a WASM/gl4es viewport restore problem triggered by out-of-bounds menu model subviews
  - if nothing changes, the leak is probably deeper than simple viewport bounds and may involve gl4es internal state caching or another transform/state path

### 0.13. Move the clamp down to the actual GL viewport/scissor submission point and remove noisy probes

- follow-up reasoning:
  - the earlier browser tab had not reloaded onto the first clamp build, so the old `viewport=` logs were still coming from the previous binary
  - the stronger fix is to clamp at the last possible stage before calling `qglViewport` / `qglScissor`, not just when `viewParms` are prepared
- code change:
  - add a WASM-only helper in `iortcw/code/renderer/tr_backend.c`
  - in `SetViewportAndScissor()`, if `backEnd.refdef.rdflags & RDF_NOWORLDMODEL`, clamp the local viewport/scissor rect to the `glConfig` backing store bounds before issuing GL calls
  - update `backEnd.viewParms.viewport*` to the clamped values so later renderer code sees the same rectangle that GL receives
  - retain only a low-volume `WASM GL viewport clamp` log when a clamp actually changes the rect
- cleanup:
  - remove the old per-frame `WASM RE_StretchPic`, `WASM main decor`, `WASM main model`, `WASM decor quad`, and `WASM restoring 2D` logs
  - this should keep the menu responsive while leaving one targeted diagnostic at the actual suspected failure point

### 0.14. Relax the WASM viewport clamp: sanitize the origin, preserve the intended subview size

- follow-up observation from the visible menu build:
  - full clamping to the canvas bounds made the menu visible again, but it also visibly compressed the composition
  - the top flame/logo pass looked clipped or displaced, and the menu text still had debug-induced overdraw
- revised hypothesis:
  - the real gl4es/WASM hazard may be the negative viewport origin, not the oversized width/height by themselves
  - if so, forcing the width/height to shrink to the canvas is overcorrecting and distorting the intended menu subviews
- code change:
  - in both `tr_scene.c` and `tr_backend.c`, WASM viewport sanitization now clamps only negative `x/y` to `0`
  - keep the original positive `width/height` unless they are invalid (`< 1`)
  - remove the remaining font and dark-fill diagnostics that could make menu text appear doubled or otherwise misleading during visual comparison
- expected result:
  - menu should stay visible
  - model-driven decorative passes should render closer to native scale and placement than the stricter full-screen clamp variant

### 0.15. Relaxed clamp was not sufficient; revert to visible-bounds clamp and hard-reset 2D viewport/scissor

- outcome of the relaxed clamp:
  - it preserved more of the original `RDF_NOWORLDMODEL` width/height
  - but the large background flag and bottom flame composition drifted back toward the wrong placement
  - this suggests the strict visible-bounds clamp was a better approximation of how native GL clips those negative-origin subviews
- next adjustment:
  - restore the stricter clamp in both `tr_scene.c` and `tr_backend.c`
  - in `RB_SetGL2D()`, add a WASM-only viewport/scissor reset sequence:
    - first set `1x1`
    - then set the real `glConfig.vidWidth x glConfig.vidHeight`
- rationale:
  - if gl4es/WebGL is retaining stale viewport/scissor state across the NOWORLDMODEL -> 2D transition, forcing an intermediate tiny viewport may break the cache path more reliably than reissuing only the final fullscreen values

### 0.16. Minimal post-reset quad probe

- since the hard reset improved the visible result but did not fully fix composition, the next question is narrower:
  - after the `1x1 -> fullscreen` reset, do the first 2D menu decor quads finally see fullscreen `GL_VIEWPORT` / `GL_SCISSOR_BOX`, or do they still inherit subview state?
- code change:
  - add a low-volume WASM log in `RB_StretchPic()` for the first few menu decor shaders only
  - log:
    - shader name
    - rect
    - `backEnd.projection2D`
    - current `GL_VIEWPORT`
    - current `GL_SCISSOR_BOX`
- intent:
  - decide whether the remaining mismatch is still a viewport/scissor restore problem
  - or whether the restore is now correct and the next bug is in model/menu composition itself

### 0.17. Removed debug-side visual contamination from the WASM build

- user report:
  - multiplayer menu still showed artifacts drawn over menu items
  - flags appeared pushed inward on top of each other
  - top logo flame pass still looked misplaced/clipped
- important audit result:
  - `R_CreateBuiltinImages()` had a WASM-only modification that changed the built-in `*white` helper texture from solid white to a tinted semi-transparent texture
  - that helper is used broadly for fills / simple textured quads, so it could contaminate unrelated menu visuals and make debugging misleading
- cleanup performed:
  - restore the built-in `*white` texture to true solid white
  - remove the temporary WASM image-upload logs in `tr_image.c`
  - remove the temporary WASM UI shader-registration / init logs in `ui_main.c`
  - remove the temporary white-shader texcoord rewrite and post-reset quad logging in `tr_backend.c`
- result:
  - the deployed build should now reflect only the real renderer/menu issues, not debug-induced helper-texture or probe side effects

### 0.18. Compensate the projection matrix for clamped NOWORLDMODEL subviews

- remaining real symptom after debug cleanup:
  - menu text/fills improved
  - but the left/right flag model and the bottom burn/logo model were still pulled inward toward the center
- interpretation:
  - strict viewport clamping keeps the WASM frame visible, but shrinking those negative-origin model subviews changes their apparent screen placement
  - the correct emulation is: clamp what WebGL cannot accept, then compensate the projection so the model still lands where the original unclamped viewport would have placed it
- code change:
  - add `unclampedViewportX/Y/Width/Height` to `viewParms_t`
  - populate them in `RE_RenderScene()` before the WASM viewport clamp
  - in `R_SetupProjection()`, when the unclamped and clamped viewport rects differ, scale and offset the projection matrix rows so the effective window mapping matches the original off-screen subview
- expected result:
  - flag and burn/logo decorative model items should move back toward their native edge placement
  - without reintroducing the broken WASM viewport/scissor leak

### 0.19. Projection compensation also had to update the frustum planes

- follow-up result from the first compensated build:
  - menu composition improved, but the flags still collapsed toward center
  - the burn/logo model behind the bottom wolf icon still looked clipped and vertically wrong
- root cause refinement:
  - `R_SetupProjection()` was adjusting the projection matrix for clamped NOWORLDMODEL subviews
  - but `R_SetupFrustum()` still built cull planes from the old symmetric `xmin/xmax/ymax` values
  - that left those menu-model scenes with a projection/frustum mismatch on WASM
- code change:
  - update `R_SetupFrustum()` to accept `ymin` as well as `ymax`
  - in `R_SetupProjection()`, when WASM viewport compensation is active, derive the compensated `xmin/xmax/ymin/ymax`
  - build frustum planes from the compensated extents instead of the pre-clamp symmetric ones
- observed result after rebuild, deploy, reload, and screenshot:
  - the left and right flags moved back out toward the edges
  - the bottom burn/logo model stopped collapsing into the center wedge shape
  - the remaining menu layout now looks much closer to the native Linux reference

### 0.20. Multiplayer now connects; next blocker is the real 3D world renderer

- current state changed materially after the earlier menu/UI work:
  - browser multiplayer join now succeeds
  - audio confirms the client is live in-session
  - spectating / snapshot advancement appears real
- the remaining symptom is not a pure "black screen" anymore:
  - the user can sometimes tell that objects are moving
  - the scene is still too dark / malformed / indistinct to be usable
  - stale text/pixels can remain on-screen, which keeps clear/state bugs in scope
- important negative results already established:
  - forcing vertex-light style experiments did not by themselves restore a correct world image
  - the earlier viewport/scissor/menu-model fixes were necessary for UI work but did not solve the in-game world frame
- working interpretation:
  - the next phase is shared 3D renderer debugging, not network bring-up and not menu-only composition work
  - likely focus areas are:
    - world-surface submission counts and stage selection
    - clear/depth behavior on the first bad in-game frame
    - fixed-function texture/lightmap combine state under gl4es/WebGL
    - effective world viewport/scissor for non-`RDF_NOWORLDMODEL` scenes
