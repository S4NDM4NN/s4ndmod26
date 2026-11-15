# WASM 3D World Renderer Troubleshoot

## Current Status (as of 2026-06-30)

This file had accumulated several rounds of diagnostics, and some earlier
conclusions were later disproven. The current high-confidence state is:

- Browser multiplayer is connected and live
- BSP world surfaces are submitted through `RB_StageIteratorGeneric`
- WASM is running the fixed-function, single-texture, 2-pass lightmap path
- Matrix readback/query functions in this stack are not trustworthy diagnostics
- A `glGetFloatv(GL_PROJECTION_MATRIX)` diagnostic call likely corrupted later
  world draws by disturbing gl4es internal matrix state
- The current checkout still contains active diagnostics and is NOT a clean fix

## Current Checkout State

The working tree is currently in diagnostic mode:

- `iortcw/code/renderer/tr_backend.c`
  - skips non-`RDF_NOWORLDMODEL` 3D world rendering entirely
  - still contains WASM red-clear diagnostics
- `iortcw/code/client/cl_scrn.c`
  - has `fprintf(stderr, ...)` probes before `Con_DrawConsole()`
- `iortcw/code/client/cl_console.c`
  - has `fprintf(stderr, ...)` probes inside `Con_DrawConsole()`
- `iortcw/code/renderer/tr_shade.c`
  - reloads `GL_MODELVIEW` immediately before draw on WASM
  - no longer does `glGetFloatv` matrix readback in the draw audit

Anyone continuing from here should first distinguish:

1. The suspected root cause for dark/invisible world geometry
2. The separate in-game console / 2D rendering issue
3. Temporary diagnostics that are intentionally distorting normal rendering

## Current Symptom (as of 2026-06-29)

Browser multiplayer is connected and live (audio, snapshots, spectating). The
world image is wrong:

- The scene is very dark, nearly black
- Some objects (likely entity models / bots) appear white or over-bright
- Stale text/pixels can remain, suggesting incomplete clears
- Earlier viewport/scissor fixes solved the menu but did not solve the world render

## Background: What Was Already Ruled Out

- Not a networking / connect issue
- Not a pak / pure-check issue
- Not a menu viewport/scissor leak (that was a separate bug, now fixed)
- Forcing `r_vertexLight` alone did not restore a correct image (earlier test,
  but conditions have changed since those menu bugs were also active)

---

## Architecture Facts (from code, confirmed)

### Multitexture is permanently disabled in WASM

`iortcw/code/sdl/sdl_glimp.c` (EMSCRIPTEN path, around line 1022):

```c
qglMultiTexCoord2fARB = NULL;
qglActiveTextureARB   = NULL;
qglClientActiveTextureARB = NULL;
glConfig.numTextureUnits = 1;
```

**Consequence**: `CollapseMultitexture()` in `tr_shader.c` always returns false
(`!qglActiveTextureARB`). No shader gets `multitextureEnv` set. Therefore:

- `RB_StageIteratorLightmappedMultitexture` is **never** selected for any shader
- World BSP surfaces fall through to `RB_StageIteratorGeneric`

### World BSP surface rendering (2-pass generic path)

For a standard lightmapped BSP surface with two shader stages:

```
Stage 0: { map $lightmap; rgbGen identity; tcGen lightmap }
Stage 1: { map textures/...; blendFunc GL_DST_COLOR GL_ZERO; tcGen texture }
```

In `RB_IterateStagesGeneric`:

1. **Stage 0**: lightmap bound, `GL_State(GLS_DEFAULT)` → depth write ON,
   depth test LEQUAL, no blending → rasterizes lightmap to color + depth buffer
2. **Stage 1**: diffuse texture, blend `GL_DST_COLOR, GL_ZERO`
   (multiply: `result = diffuse * lightmap`) → depth test LEQUAL, depth write OFF

This is a classic multi-pass lightmap approach. It requires the depth values of
stage 1 fragments to exactly match stage 0, so the `LEQUAL` depth test passes.

### Entity model rendering

Entity models (`CGEN_LIGHTING_DIFFUSE`) use `RB_StageIteratorVertexLitTexture`.
Vertex colors come from `R_SetupEntityLightingGrid` sampling the BSP light grid,
then scaled by `tr.identityLightByte`.

With `tr.overbrightBits = 0` (set because `!glConfig.deviceSupportsGamma` and
not fullscreen), `tr.identityLight = 1.0` and `tr.identityLightByte = 255`.
This means entity lighting uses the full range of the grid values without any
overbright divide.

---

## Leading Theories for Dark World

### Theory A: matrix mode mismatch at upload boundary

Status: superseded by later findings.

The engine-side source matrices are sane. `glGetFloatv(GL_MODELVIEW_MATRIX)` and
`glGetFloatv(GL_PROJECTION_MATRIX)` readbacks are garbage — but entity models
ARE rendering at correct screen positions (white, but visible). This means
`glGetFloatv` for matrix enums is NOT a reliable indicator of rendering state
in gl4es/WebGL. The readback probes were misleading.

The remaining matrix concern: `qglMatrixMode` itself might silently fail in gl4es,
causing a projection matrix upload to land in the modelview stack (or vice versa).
A `glGetIntegerv(GL_MATRIX_MODE)` probe has been added to `RB_LogWasmMatrixUpload`
to verify the active mode at the moment of each upload.

Expected output:
```
WASM matrix mode: want=0x1701 got=0x1701   ← projection upload, mode correct
WASM matrix mode: want=0x1700 got=0x1700   ← modelview upload, mode correct
```

If `got` does not match `want`, the upload goes to the wrong stack → fix by
calling `qglMatrixMode(matrixMode)` immediately inside the upload rather than
relying on the caller to set it first.

### Theory B: 2-pass depth test failure

Status: ruled out by later testing.

`qglDepthFunc(GL_ALWAYS)` for stage 1 is already in `tr_shade.c` (WASM-only,
`stage > 0 && lightmapIndex >= 0` guard). World is still dark with this in
place. This theory is eliminated.

### Theory C: Lightmap multiply blend broken in WebGL

Status: still possible historically, but not the leading theory after later
matrix-query findings.

`GL_DST_COLOR, GL_ZERO` is a legal OpenGL blend mode and legal in WebGL. However
if gl4es mishandles the destination color factor under the current framebuffer
format or state, stage 1 could render incorrectly (black, or unchanged framebuffer).

**Test**: Force `GL_MODULATE` texenv on TMU0 for stage 1 instead of the blend
approach, with `qglDepthFunc(GL_LEQUAL)`. If world brightens, the blend equation
was the problem.

### Theory D: Lightmap values intrinsically too dark (no overbright)

Status: secondary possibility only; does not explain the stronger clipping-style
symptoms seen later.

Without hardware gamma or overbright bits, lightmap texels are taken as-is.
If the loaded lightmap pixels are dark (which is normal for pre-baked interior
lighting), the world will appear dark but not completely black.

This does NOT explain "completely black" if the 2-pass combine is working at all.
But if combined with theory A or B, very dark lightmap values → near-black world.

**Check**: Enable `r_lightmap 1` cvar (lightmap-only view). If world becomes
visible as colored patches (even dark), lightmap data is being sampled. If still
black, the lightmap texture itself is not being bound or uploaded correctly.

---

## Leading Theory for White Objects (Entity Models)

Entity models use `RB_StageIteratorVertexLitTexture`. Vertex colors are:
`ambient + directed * dot(normal, lightDir)`, all scaled by `identityLightByte = 255`.

If the light grid lookup returns high values (full-bright ambient), entities
appear white or over-bright even when the world is dark. This can happen if:

- The map's light grid has very high ambient values
- `R_SetupEntityLightingGrid` returns a fallback full-white value when the entity
  origin is outside the grid bounds

**Check**: Log `ent->ambientLight` / `ent->directedLight` in `R_SetupEntityLightingGrid`
for the first few entities (see diagnostic additions below).

---

## Diagnostic Probes Added (2026-06-29)

### Already present (from previous session work)

**`iortcw/code/renderer/tr_world.c`** — `R_AddWorldSurfaces`:
```
WASM world surfaces: added=N total=N leafs=N dlights=N rdflags=0x...
```
Fires for first 6 world frames. Tells us whether BSP geometry is being submitted.

**`iortcw/code/renderer/tr_scene.c`** — `RE_RenderScene` (non-NOWORLDMODEL):
```
WASM world view: refdef=X,Y WxH fov=.../... viewport=... rdflags=...
```
Tells us what refdef the cgame is submitting for the 3D world.

**`iortcw/code/renderer/tr_backend.c`** — `RB_BeginDrawingView` (non-NOWORLDMODEL):
```
WASM world begin: clear=0x... ui=N portal=N ... view=... glViewport=... glScissor=...
```
Tells us clear bits, viewport, scissor at the start of the 3D draw.

### New probes (added this session)

**`iortcw/code/renderer/tr_shade.c`** — which shader iterator fires for world:
```
WASM generic: shader=... lm=N passes=N env=N
WASM lm_multi: shader=... rdflags=...      (should NOT appear for world)
WASM vlit: shader=... entity=N             (entities only)
```

**`iortcw/code/renderer/tr_shade.c`** — per-stage GL state:
```
WASM stage N: lm=N state=0x... bundle1=ptr
```
Confirms stage 0 is lightmap, stage 1 is diffuse + blend.

**`iortcw/code/renderer/tr_light.c`** — entity lighting values:
```
WASM entlight: ambient=R,G,B directed=R,G,B identByte=N
```

### Added 2026-06-30

**`iortcw/code/renderer/tr_backend.c`** — matrix upload/readback audit:
```
WASM matrix upload projection:view #N src0=(...) src1=(...)
WASM matrix upload projection:view src2=(...) src3=(...)
WASM matrix readback projection:view gl0=(...) gl1=(...)
WASM matrix readback projection:view gl2=(...) gl3=(...)

WASM matrix upload model:world #N src0=(...) src1=(...)
WASM matrix upload model:world src2=(...) src3=(...)
WASM matrix readback model:world gl0=(...) gl1=(...)
WASM matrix readback model:world gl2=(...) gl3=(...)
```

Purpose:
- capture the exact matrix passed into `qglLoadMatrixf`
- compare it to GL state immediately after upload
- determine whether corruption happens before upload or at/after upload

---

## Quick Experiments (Ordered by Expected Value)

1. **Instrument below `qglLoadMatrixf` / inside gl4es matrix path**
   - The strongest current evidence points at the matrix upload boundary itself
   - Add logging or assertions in the gl4es `glLoadMatrixf` / matrix-stack code
   - Verify whether the submitted float array is received intact and whether the
     active matrix mode is what we think it is

2. **Log matrix mode and stack transitions around world/entity/2D switches**
   - We now know the source matrices are good
   - We do not yet know whether the wrong matrix stack is active or being reused
   - Especially important at:
     - `SetViewportAndScissor()`
     - entity/world switches in `RB_RenderDrawSurfList()`
     - `RB_SetGL2D()`

3. **`r_lightmap 1`** cvar in console / server.cfg
   - Shows lightmap only (skips diffuse stage)
   - If world becomes visible as dark patches → stage 0 is working, stage 1 is the problem
   - If still black → lightmap textures are not being sampled at all

4. **Force `GL_ALWAYS` depth for stage 1** (WASM-only code change)
   - In `RB_IterateStagesGeneric`, for non-lightmap stages in world, temporarily force
     `qglDepthFunc(GL_ALWAYS)` before the draw, restore `GL_LEQUAL` after
   - If world becomes visible → depth test failure between passes confirmed (Theory A)

5. **Force `r_vertexLight 1`** (requires map reload / reconnect)
   - Bypasses lightmaps entirely; world uses vertex colors from BSP vertex color data
   - If world becomes visible → lightmap path is broken; vertex fallback works
   - Note: `r_vertexLight` requires map reload to take effect (`R_LoadLightmaps` skips loading)

6. **Log entity lighting** (from new probe)
   - If `ambientLight` values come out near 255 for R/G/B → entities are full-bright → expected white
   - If values are low but entities still appear white → something else is setting vertex colors

7. **Check `glConfig.deviceSupportsGamma`** — print this at renderer init
   - If 0 (expected for WASM), overbright is 0, which is correct
   - If somehow 1, the lightmap gamma tables would be applied to uploaded textures

---

## Files to Focus On

| File | Why |
|------|-----|
| `iortcw/code/renderer/tr_shade.c` | 2-pass world rendering, entity iterator, stage draw |
| `iortcw/code/renderer/tr_light.c` | Entity lighting grid lookup |
| `iortcw/code/renderer/tr_bsp.c` | Lightmap load and texture upload |
| `iortcw/code/renderer/tr_backend.c` | Clear, depth state, `SetViewportAndScissor` |
| `iortcw/code/renderer/tr_world.c` | Surface submission count |
| `iortcw/code/sdl/sdl_glimp.c` | Multitexture disable decision (around line 1022) |

---

## Notes on What NOT to Re-investigate

- WebSocket / UDP bridge / networking — solved
- Menu viewport/scissor leak — solved (NOWORLDMODEL clamp + projection compensation in place)
- Pak checksum / pure check — not the current issue
- Canvas sizing / HTML shell presentation — fixed earlier

---

## Session Log

## Condensed Timeline

### What is still believed

- `cg_viewsize` was incorrectly persisted low in browser config during one phase
  and did produce a tiny centered world viewport
- BSP world surfaces are reaching the classic generic 2-pass renderer
- Multitexture is disabled in the WASM path, so lightmapped BSP surfaces do not
  use the multitexture iterator
- Matrix readback/query APIs in this stack are unreliable enough to poison
  debugging conclusions
- Removing `glGetFloatv(GL_PROJECTION_MATRIX)` from the draw audit is a correct
  cleanup regardless of whether it is the full root cause

### What was later disproven or weakened

- The earlier “console is rendering but clipped by leaked scissor” conclusion
  was weakened by a later test that disabled 3D world rendering entirely and
  still did not restore the console
- The earlier “matrix mode is broken” conclusion was too broad; later notes
  narrow the issue to matrix query/readback behavior, not necessarily
  `glMatrixMode()` itself
- The earlier `+seta r_lightmap 1` shell-argument experiment was not reliable
  evidence because the cvar did not take effect in this startup path

### Recommended next step from this document

Treat the problems as two separate tracks:

1. Re-enable normal 3D rendering and confirm whether removing matrix readback
   side effects plus the per-draw modelview reload makes world BSP visible.
2. Independently verify whether `SCR_DrawScreenField()` and
   `Con_DrawConsole()` are reached in-game using the stderr probes, because the
   console issue was not resolved by disabling 3D world rendering.

### 2026-06-29 — session start

**Already committed probes** (from previous work, now targeting world render):
- `tr_world.c` — `R_AddWorldSurfaces` surface count (`WASM world surfaces: ...`)
- `tr_scene.c` — `RE_RenderScene` refdef/viewport for world views (`WASM world view: ...`)
- `tr_backend.c` — `RB_BeginDrawingView` clear bits + GL viewport/scissor (`WASM world begin: ...`)

**New probes added this session** (`tr_shade.c`, uncommitted):
- `RB_StageIteratorGeneric`: logs first 12 world-surface generic draws
  → confirms multitexture collapse did NOT happen (expected), shows lightmap index and pass count
- `RB_IterateStagesGeneric` stage loop: logs first 24 per-stage state dumps
  → shows which stage is lightmap, what blend bits are set, whether bundle[1] is populated
- `RB_StageIteratorLightmappedMultitexture`: logs if this ever fires for world
  → should NOT fire (multitexture is disabled); if it does, something changed

### 2026-06-30 — matrix upload audit result

New probe added in `tr_backend.c` around the real world-matrix upload sites:

- projection load in `SetViewportAndScissor()`
- modelview load during entity/world switches in `RB_RenderDrawSurfList()`
- final restore of world modelview after drawsurf processing

Result summary:

- Projection source matrix is stable and correct every frame
- World modelview source matrix is sane and plausible for camera orientation/position
- Immediate GL readback after `qglLoadMatrixf()` is already wrong
- First projection/modelview readbacks were all zeros
- Later projection readbacks changed each frame even though source projection did not
- Later entity modelview readbacks sometimes became identity-like or unrelated transforms
- World draw audits continue to show sane geometry, texcoords, and indices

Representative evidence:

```text
WASM matrix upload projection:view #0 src0=(1.000 0.000 0.000 0.000) src1=(0.000 1.333 0.000 0.000)
WASM matrix upload projection:view src2=(0.000 0.000 -1.001 -8.005) src3=(0.000 0.000 -1.000 0.000)
WASM matrix readback projection:view gl0=(0.000 0.000 0.000 0.000) gl1=(0.000 0.000 -0.000 0.000)
WASM matrix readback projection:view gl2=(0.000 0.000 0.000 0.000) gl3=(0.000 0.000 0.000 0.000)

WASM matrix upload model:world #0 src0=(0.521 -0.853 0.000 1615.547) src1=(-0.040 -0.025 0.999 -343.479)
WASM matrix upload model:world src2=(-0.852 -0.521 -0.047 863.878) src3=(0.000 0.000 0.000 1.000)
WASM matrix readback model:world gl0=(0.000 0.000 0.000 0.000) gl1=(0.000 0.000 0.000 0.000)
WASM matrix readback model:world gl2=(0.000 0.000 0.000 0.000) gl3=(0.000 0.000 0.000 0.000)
```

Interpretation:

- The problem is not bad world vertex data
- The problem is not cgame building a nonsense refdef matrix
- The corruption is happening at or below the fixed-function matrix upload path
- This also explains why in-game 2D and world rendering can both fail: both depend
  on matrix/state setup, even though they use different high-level code paths

Current best next step:

- audit or instrument the gl4es matrix stack / `glLoadMatrixf` implementation
- verify active matrix mode and stack selection at the point gl4es receives the call
- `RB_StageIteratorVertexLitTexture`: logs entity ambient/directed light values
  → will confirm whether "white objects" are entities with full-bright grid lighting

**Entity lighting probe still to add** (`tr_light.c`):
- Optional: log `ambientLight` / `directedLight` in `R_SetupEntityLightingGrid`
  (the `WASM vlit` probe in `tr_shade.c` already captures final values from `backEnd.currentEntity`)

**Live diagnostic capture — 2026-06-29**:

```
WASM world view: refdef=224,168 192x144 fov=90.0/73.7 viewport=224,168 192x144 unclamped=224,168 192x144 rdflags=0x0
WASM world surfaces: added=1118 total=1118 leafs=421 dlights=3 rdflags=0x0
WASM world begin: clear=0x100 ui=0 portal=0 fastsky=0 view=224,168 192x144 glViewport=224,168 192x144 glScissor=224,168 192x144
```

**What this told us:**
1. BSP surfaces are being submitted correctly (1118 surfaces, rdflags=0x0).
2. The world viewport is `192×144` at `(224,168)` — exactly `cg_viewsize=30` (minimum, centered on 640×480).
3. The depth clear (`0x100`) but no color clear means everything outside 192×144 stays dark.
4. HUD and console also not rendering — same scissor box leaks into 2D passes.

**Root causes found at that time (later partially superseded):**

**Bug A — `cg_viewsize = 30` (saved in browser IDBFS config)**
- The cgame's world viewport calculation (`CG_CalcVrect`) correctly uses cg_viewsize.
- With cg_viewsize=30 (the minimum), refdef = `(640-192)/2, (480-144)/2  192×144`.
- This was saved from a prior session where the viewsize key was pressed to minimum.

**Bug B — Scissor leak from world render into 2D (later weakened by newer diagnostics)**
- `RB_DrawSurfs()` only called `RB_SetGL2D()` after `RDF_NOWORLDMODEL` scenes.
- After a real world render, the GL scissor stayed at the world's sub-viewport.
- `RB_SetGL2D()` was only called lazily from `RB_StretchPic()`, but gl4es/WebGL
  doesn't reliably apply the `qglViewport`/`qglScissor` reset in that path.
- Result: HUD draws and console draws were scissored to the 192×144 world box.
- User symptom: "when I open the console I can see colors changing behind artifacts" —
  the console WAS rendering, just clipped to the tiny world viewport region.

**Fixes applied (2026-06-29):**

1. `iortcw/code/renderer/tr_backend.c` — `RB_DrawSurfs()`:
   - Changed the `if (RDF_NOWORLDMODEL) { RB_SetGL2D(); }` to `RB_SetGL2D()` unconditionally,
     followed by `backEnd.projection2D = qfalse`.
   - Forces viewport/scissor reset after ANY 3D render pass, not just menu-model ones.
   - `projection2D = qfalse` ensures the first StretchPic still enters 2D setup correctly.

2. `wasm/shell.html` — Module arguments:
   - Added `+cg_viewsize 100` to startup arguments so the cvar is forced to full-screen
     on every load, overriding any saved browser config value.

**Second rebuild needed — 2026-06-29:**

After first rebuild, the shade probes fired only 2 times (both entity draws in the main menu, before connecting). They did NOT fire for any BSP world surfaces after connecting. Root-cause unknown:

- Possibility A: BSP world draws have `backEnd.refdef.rdflags = RDF_NOWORLDMODEL` even though the world-begin probe logged `rdflags=0x0`
- Possibility B: BSP world draws do not go through `RB_StageIteratorGeneric` (possibly routed to `RB_StageIteratorLightmappedMultitexture` despite multitexture being disabled, or to `RB_StageIteratorVertexLitTexture`)
- Possibility C: probe counter was already saturated before world rendered (unlikely — counter was at 2 of 12)

**Changes made for second rebuild:**

1. `wasm/shell.html` — fixed `+cg_viewsize` to `+seta cg_viewsize 100`
   - `+cg_viewsize 100` only PRINTS the value (wrong)
   - `+seta cg_viewsize 100` SETS and archives it (correct)
   - This should fix the 192×144 world viewport → 640×480

2. `iortcw/code/renderer/tr_shade.c` — removed `!NOWORLDMODEL` guard from all probes:
   - `RB_StageIteratorGeneric` probe: now logs first 20 calls regardless of rdflags, includes `rdflags=0x...` in output
   - `RB_IterateStagesGeneric` stage probe: now logs first 40 stage calls, includes rdflags
   - `RB_StageIteratorVertexLitTexture` probe: now logs first 12, includes rdflags

After next rebuild, look for:
- `WASM generic:` with `rdflags=0x0` and `lm>=0` → BSP world draws going through StageIteratorGeneric (expected)
- `WASM generic:` with `rdflags=0x1` → NOWORLDMODEL draws (menu models)
- `WASM vlit:` with `rdflags=0x0` → entity draws in world context
- Any `WASM lm_multi:` entries → would indicate multitexture iterator is running (should NOT happen)
- If NO `WASM generic:` with `lm>=0` appears → BSP world shaders routed to vlit or lm_multi unexpectedly

**Next steps after second rebuild:**
1. Run `make wasm-build && make wasm-deploy`
2. Hard-refresh browser, connect to server
3. Check new output: does `WASM world view` now show `refdef=0,0 640x480`? (viewport fix)
4. After ~1 second of world rendering, open browser console and share the WASM probe output
5. Use the rdflags column to separate menu renders (0x1) from world renders (0x0)

---

### 2026-06-30 — second rebuild result

**Viewport**: `WASM world view: refdef=0,0 1024x768` — the `cg_viewsize` fix is confirmed working.

**BSP world surfaces confirmed through generic path**:
- `WASM generic: shader=... lm=N passes=2 entity=0 rdflags=0x0` firing for many world shaders ✓
- Stage 0: `lm=1 state=0x100` (lightmap, no blend, depth write) ✓
- Stage 1: `lm=0 state=0x13` (diffuse, `GL_DST_COLOR GL_ZERO` multiply blend) ✓
- 1118 BSP surfaces submitted ✓

**Theory B (depth test) ruled out**: `GL_ALWAYS` for stage 1 is already in `tr_shade.c`.
World is still dark. Depth test failure is not the cause.

**Matrix readback probes are misleading**: `glGetFloatv(GL_MODELVIEW_MATRIX)` returns
garbage in gl4es — zeros on frame 0, junk on later frames. But entity models render
at correct screen positions (they appear as white blobs in the world), so the rendering
path is using correct matrices internally. `glGetFloatv` does not reflect the
actual rendering state in gl4es/WebGL.

**`glGetFloatv` inside `R_DrawElements` audit**: The `glmodel` and `glproj` audit values
(including the `36893488147419103232 = 2^65` in the projection) are all from this same
broken `glGetFloatv` path. They do NOT prove the rendering projection is wrong.

**Changes for next test run (2026-06-30)**:

1. `iortcw/code/renderer/tr_backend.c` — `RB_LogWasmMatrixUpload`:
   - Added `glGetIntegerv(GL_MATRIX_MODE, &currentMode)` probe
   - Emits: `WASM matrix mode: want=0x1701 got=0x1701` (projection) or `want=0x1700 got=0x1700` (modelview)
   - If `got ≠ want` → `qglMatrixMode` is broken → uploads go to wrong stack
   - If `got == want` → matrix mode is correct → matrix path is not the problem

2. `wasm/shell.html` — added `+seta r_lightmap 1` (temporary diagnostic):
   - Breaks after stage 0 (lightmap only), skips stage 1 blend
   - If world shows any colored/gray patches → lightmap textures are uploading and binding OK
     → issue is in stage 1 blend (Theory C) or lightmap values too dark (Theory D)
   - If world still completely black → lightmap texture bind or upload is broken

**Results from 2026-06-30 third run**:

- `WASM matrix mode: want=0x1701 got=0xffffffff` — `glGetIntegerv(GL_MATRIX_MODE)` returns -1 (not implemented in gl4es). This made the matrix-mode-logging approach a dead end. Later evidence suggests the query/readback APIs are the broken part, not necessarily `glMatrixMode()` itself.
- `+seta r_lightmap 1` in shell.html args DID NOT TAKE EFFECT — both stage 0 and stage 1 ran for every shader. Renderer cvars set via `+seta` before renderer init may not stick. Cvar-based testing is unreliable for renderer cvars in this WASM build.

**Changes for next test run (2026-06-30 fourth build)**:

1. `iortcw/code/renderer/tr_backend.c` — `RB_BeginDrawingView`:
   - Added red color clear for first 3 world-render frames (WASM only)
   - After the normal depth clear, clears color buffer to RED for first 3 frames
   - Expected screen behavior:
     - **All or mostly red** → BSP geometry is being clipped (wrong matrix or near-plane issue)
     - **Red partially covered by dark areas** → BSP geometry renders to those areas but is dark (lightmap/blend issue)
     - **No red visible, all dark** → BSP geometry covers entire viewport but is dark
   - The red-clear fires for the first 3 frames only (static counter), then normal rendering resumes

2. `iortcw/code/renderer/tr_shade.c` — `RB_IterateStagesGeneric`:
   - Added WASM-only break after stage 0 for world lightmapped surfaces
   - Replaces the broken `r_lightmap 1` cvar approach (cvar didn't work via `+seta`)
   - This means stage 0 (lightmap) runs and stage 1 (diffuse multiply) is SKIPPED
   - Combined with the red clear:
     - Red covered by VISIBLE lightmap patches → lightmaps work, blend (stage 1) was failing
     - Red covered by DARK/BLACK areas → lightmap texture data is dark or not bound
     - Red with no coverage → geometry not reaching screen (matrix issue)

3. `wasm/shell.html`: Removed `+seta r_lightmap 1` (didn't work).

**Results from 2026-06-30 fifth run** (red-clear + lightmap-only break):

User reported: **"it flashed red -- then all went black"**

Interpretation: The screen was PURE RED for the first 3 frames, then returned to the normal dark black. World BSP geometry produced NO opaque pixels — the geometry was NOT covering the red background. After the 3 red frames the background reverted to the normal dark render.

This strongly suggested world BSP geometry was not producing visible coverage.
At the time this was interpreted as clipping; later investigation connected it
more specifically to matrix-query side effects in the debug path.

**Matrix analysis from latest logs** (2026-06-30):

The probe prints matrices ROW by ROW (`srcMatrix[0,4,8,12]` = row 0, etc.). Decoding the world modelview:
```
Row 0: ( 0.521, -0.853,  0.000, 1615.547)  ← M[0][3]=tx=1615 ✓
Row 1: (-0.040, -0.025,  0.999, -343.479)  ← M[1][3]=ty=-343 ✓
Row 2: (-0.852, -0.521, -0.047,  863.878)  ← M[2][3]=tz=863  ✓
Row 3: ( 0.000,  0.000,  0.000,   1.000)   ← bottom row 0,0,0,1 ✓
```

Manual MVP transform of audit vertex (3498, 3236, 1310, 1):
- Eye space: z = -3864 (NEGATIVE = in front of camera) ✓
- NDC: x=0.175, y=0.257 (solidly within viewport) ✓

**The matrix VALUES are correct.** With the correct matrices, world geometry SHOULD be visible on screen. The fact that it is NOT visible means gl4es is not applying the uploaded world modelview to world surface draws.

**Key asymmetry**: Entity models (white bots) ARE visible. Each entity uploads its own matrix immediately before drawing. World surfaces share ONE matrix upload at the start of the frame. gl4es appears to not apply a matrix that was uploaded earlier in the frame to subsequent draw calls, but DOES apply matrices that were uploaded immediately before each draw.

**Root cause theory at that time**: gl4es had a deferred or lazy matrix commit
behavior. This was later replaced by a more specific theory tied to
`glGetFloatv(GL_PROJECTION_MATRIX)` side effects.

**Fix applied (2026-06-30)**:

`iortcw/code/renderer/tr_shade.c` — `RB_StageIteratorGeneric`, before `RB_IterateStagesGeneric`:

```c
#ifdef __EMSCRIPTEN__
qglMatrixMode( GL_PROJECTION );
qglLoadMatrixf( backEnd.viewParms.projectionMatrix );
qglMatrixMode( GL_MODELVIEW );
qglLoadMatrixf( backEnd.or.modelMatrix );
#endif
```

Forces both projection and modelview to be uploaded immediately before every world surface draw batch. Since `backEnd.or.modelMatrix` at this point equals the world camera matrix (for world entity surfaces), this ensures gl4es receives the matrix adjacent to the draw call it needs to apply to.

**Diagnostic state for next build**:
- Red clear still active (first 3 frames, `RB_BeginDrawingView`)
- Lightmap-only break still active (`RB_IterateStagesGeneric`)
- Matrix force-reload NOW ACTIVE (before `RB_IterateStagesGeneric` in `RB_StageIteratorGeneric`)

**Expected result if fix works**:
- Frames 1-3: RED background with world geometry visible (lightmap-colored patches over red)
- Frame 4+: World visible in lightmap-only mode (dim but structured)

**If world is still invisible**: gl4es is ignoring the matrix even when uploaded immediately before draw → deeper gl4es shader compilation bug needed.

---

### 2026-06-30 — sixth build: dual-mode-switch force-reload failed; narrower root-cause theory

**The dual-mode-switch fix did NOT work.**

The matrix reload in `RB_StageIteratorGeneric` uploaded:
```c
qglMatrixMode( GL_PROJECTION );
qglLoadMatrixf( backEnd.viewParms.projectionMatrix );
qglMatrixMode( GL_MODELVIEW );
qglLoadMatrixf( backEnd.or.modelMatrix );
```
World was still invisible (black screen with entity artifacts). A green vertex color
diagnostic was added to verify geometry reaches the rasterizer: `rgba(0,255,0,255)` was
confirmed in the audit for all vertices, but NO green appeared over the red background.
This proved the geometry is being CLIPPED, not failing a texture/blend step.

**Current leading theory: `glGetFloatv(GL_PROJECTION_MATRIX)` side effect in gl4es**

The audit block inside `R_DrawElements` called both:
```c
glGetFloatv( GL_MODELVIEW_MATRIX, modelView );   // line A
glGetFloatv( GL_PROJECTION_MATRIX, projection ); // line B  ← THE BUG
```
These two lines ran BEFORE the matrix reload `qglLoadMatrixf(backEnd.or.modelMatrix)`.

In gl4es, `glGetFloatv(GL_PROJECTION_MATRIX)` has a side effect: it sets gl4es's
internal "current matrix" pointer to the PROJECTION stack and does not reset it. So:
- Line B runs → gl4es current matrix = PROJECTION
- Then `qglLoadMatrixf(worldMatrix)` → loads worldMatrix into the PROJECTION slot
- `qglDrawElements` → gl4es uses PROJECTION=worldMatrix (wrong!), MODELVIEW=stale/identity

Entity draws were completely unaffected because the audit block was guarded by:
```c
backEnd.currentEntity == &tr.worldEntity && tess.shader->lightmapIndex >= 0
```
Entity surfaces are not the world entity, so no `glGetFloatv(GL_PROJECTION_MATRIX)` call,
so no mode corruption. The entity matrix loaded at transition time stayed in MODELVIEW
and rendered correctly.

**Interpretation of `glGetIntegerv(GL_MATRIX_MODE) == 0xFFFFFFFF`**:
- The QUERY is not implemented in gl4es — it cannot read back the current mode
- But `glMatrixMode()` itself WORKS — switching to GL_MODELVIEW or GL_PROJECTION is applied correctly
- The earlier conclusion that "mode switching is broken" was wrong; only the readback is broken

**Fix applied (2026-06-30)**:

`iortcw/code/renderer/tr_shade.c` — `R_DrawElements`:

1. Removed `glGetFloatv(GL_MODELVIEW_MATRIX, ...)` and `glGetFloatv(GL_PROJECTION_MATRIX, ...)`
   from the audit block entirely (they returned garbage anyway).
2. Removed the associated `glmodel` / `glproj` readback print lines.
3. Added `qglMatrixMode(GL_MODELVIEW)` explicitly before `qglLoadMatrixf(backEnd.or.modelMatrix)`
   in the per-draw reload — defensive, ensures correct mode even if any other call corrupted it.

```c
// In R_DrawElements, immediately before qglDrawElements:
#ifdef USE_OPENGLES
#ifdef __EMSCRIPTEN__
if ( !backEnd.projection2D ) {
    qglMatrixMode( GL_MODELVIEW );
    qglLoadMatrixf( backEnd.or.modelMatrix );
}
#endif
qglDrawElements( GL_TRIANGLES, numIndexes, GL_INDEX_TYPE, indexes );
    return;
```

The dual-mode-switch block in `RB_StageIteratorGeneric` was removed at the same time
(it was also doing mode switches which could interact badly).

**Also removed**: The `RB_StageIteratorGeneric` force-reload from the previous build
(which was the broken dual-mode-switch approach).

---

### 2026-06-30 — console not rendering investigation

Console (`Con_DrawConsole`, engine tilde `~`) works in the main menu but never appears
in-game. The cgame probe showed `draw2D=0` (cgame HUD suppressed, spectator/pre-spawn
state) which explains why the HUD is absent. But the engine console should render
independently via `SCR_DrawScreenField` → `Con_DrawConsole`.

**Probe result**: `Con_DrawConsole` has a `Com_Printf` probe added that logs the first
6 calls. Zero "WASM ConDraw" entries ever appear in the browser console. This means
either `Con_DrawConsole` is not being called, OR `Com_Printf` output is not reaching
the browser console (goes to the in-game console buffer only).

**Probe switched to `fprintf(stderr, ...)`** — Emscripten definitely routes stderr to
`console.error` → browser console. Same change applied to a new probe in
`SCR_DrawScreenField` right before the `Con_DrawConsole()` call.

**3D world render disabled as diagnostic**:

Added early return in `RB_DrawSurfs` for `!RDF_NOWORLDMODEL` renders:
```c
// DIAGNOSTIC: skip 3D world render to isolate whether it corrupts 2D GL state
if ( !( cmd->refdef.rdflags & RDF_NOWORLDMODEL ) ) {
    return (const void *)( cmd + 1 );
}
```

Result: Console STILL does not appear with 3D world render completely disabled.
This weakens the earlier “console is present but clipped by 3D viewport/scissor
state” explanation. The remaining problem is either in the 2D path itself, or
in whether `SCR_DrawScreenField()` / `Con_DrawConsole()` are being reached.

**Current diagnostic state** (as of this session):

| File | Change | Purpose |
|------|--------|---------|
| `tr_backend.c` `RB_DrawSurfs` | Early return for `!NOWORLDMODEL` | Disable 3D world render (diagnostic) |
| `cl_console.c` `Con_DrawConsole` | `fprintf(stderr, ...)` probe | Confirm function is reached |
| `cl_scrn.c` `SCR_DrawScreenField` | `fprintf(stderr, ...)` probe before `Con_DrawConsole()` | Confirm SCR reaches that line |
| `tr_shade.c` `R_DrawElements` | No more `glGetFloatv` calls | Root cause fix (projection matrix corruption) |
| `tr_shade.c` `R_DrawElements` | `qglMatrixMode(GL_MODELVIEW)` before reload | Defensive mode reset |
| `tr_backend.c` red clear | Still active (30 frames) | Confirm world geometry visibility when 3D re-enabled |

**Next build will reveal**:
- If "WASM SCR_Con" appears in browser console → `SCR_DrawScreenField` is reaching line 1106, issue is downstream (2D GL path or `Con_DrawConsole` itself)
- If neither probe appears → `SCR_DrawScreenField` is not completing / not called at all from `SCR_UpdateScreen`

**Leading theory**: `Con_DrawConsole` IS being called (code flow is intact), `Con_DrawNotify` IS submitting `re.DrawStretchPic` render commands, but `RB_StretchPic` or `RB_SetGL2D` is broken in a way that produces no pixels. The `Com_Printf` output from the old probe would only appear in the in-game console buffer (which itself doesn't render), so the absence of that log output was misleading — not evidence that `Con_DrawConsole` was never called.

The `fprintf(stderr, ...)` probes will definitively settle whether the call path is intact.
