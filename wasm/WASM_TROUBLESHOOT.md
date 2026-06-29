# WASM Dynamic Linking Crash — Troubleshoot Notes

## STATUS: ROOT CAUSE FOUND AND FIXED

**Fix committed**: removed `$(B)/$(BASEGAME)/cgame.mp.$(SHLIBEXT)` and
`$(B)/$(BASEGAME)/ui.mp.$(SHLIBEXT)` from `CLIENT_LDFLAGS` in `iortcw/Makefile`
(lines ~1119-1128).

**Symptom**: `invoke_vi` → `RuntimeError: index out of bounds` immediately after
`ui.mp.wasm` is loaded (log: `found vmMain function at 0x2e7b` = table index 11899).

---

## Root Cause: Double-Loading via neededDynlibs

### What was happening

When the side modules (`cgame.mp.wasm`, `ui.mp.wasm`) are listed in the engine's
`CLIENT_LDFLAGS`, Emscripten embeds them in the engine binary's `dylink.0` section
as `neededDynlibs = ['cgame.mp.wasm', 'ui.mp.wasm']`.

At browser startup, Emscripten calls `loadDylibs()` which iterates
`neededDynlibs` and calls `loadDynamicLibrary(name, {loadAsync:true,...})` for
each. Internally this resolves the path via:

```javascript
var libFile = locateFile(libName);  // = scriptDirectory + "cgame.mp.wasm"
```

`scriptDirectory` is the directory where `index.js` is served — i.e. `/play/`.
Both `cgame.mp.wasm` and `ui.mp.wasm` ARE served at `/play/cgame.mp.wasm` and
`/play/ui.mp.wasm` (they're `COPY`'d there in the Dockerfile). So the HTTP fetch
**succeeds** and both modules are instantiated at startup with LDSO keys
`"cgame.mp.wasm"` and `"ui.mp.wasm"`.

Later, the game code calls `Sys_LoadGameDll` → `FS_FindVM` → `dlopen` with path
`FS_BuildWASMPath(gamedir, dllName)` = `"/main/cgame.mp.wasm"`.
`LDSO.loadedLibsByName["/main/cgame.mp.wasm"]` ≠ `LDSO.loadedLibsByName["cgame.mp.wasm"]` —
**different keys** — so Emscripten creates a **second DSO entry** and instantiates the
module a second time.

### Why this crashes

The two instances share a single set of `GOT.func` and `GOT.mem` WebAssembly.Global
objects (provided by the Emscripten environment):

1. **Startup (instance 1)**: `updateGOT(exports)` sets all `GOT.func.X.value` for
   every exported function (e.g. `GOT.func.trap_R_DrawStretchPic = 12050`).
   `__wasm_apply_data_relocs` sets all `GOT.mem.X.value = memoryBase1 + offset_X`.

2. **Game code (instance 2)**: `updateGOT(exports)` sees `GOT.func.X.value != 0`
   → `replace=false` → **skips** all symbols already set by instance 1.
   `__wasm_apply_data_relocs` **overwrites** all `GOT.mem.X.value = memoryBase2 + offset_X`.

Result: `GOT.func` entries still point to instance 1's functions, but `GOT.mem`
entries now point to instance 2's memory region. `dllEntry` is called only on
instance 2's handle, so instance 2's `syscall` variable (at `memoryBase2 + offset`)
gets set to `VM_DllSyscall`'s table index.

Instance 1's `syscall` (at `memoryBase1 + offset`) is **never initialized** — it
stays at the C initializer value of `-1` = `0xFFFFFFFF`.

When the game calls `vmMain(UI_INIT)` on instance 2's handle:
- `_UI_Init` stores function pointers like `&trap_Key_SetOverstrikeMode` into
  `uiInfo.uiDC.*` by reading `GOT.func.trap_Key_SetOverstrikeMode`.
- That GOT value points to **instance 1**'s `trap_Key_SetOverstrikeMode`.
- Instance 1's function reads `GOT.mem.syscall.value` which now points to
  `memoryBase2 + offset_syscall` = VM_DllSyscall's table index (dllEntry was called).
  **This part works.**
- But instance 1's function also reads other `GOT.mem` values like `GOT.mem.cvar_table`,
  `GOT.mem.uis`, etc. which were overwritten to point to `memoryBase2` data.
- Meanwhile, instance 2's `_UI_Init` also directly calls engine functions (trap_*).
  Those direct calls go to instance 2's own trap_ functions (correct), but somewhere
  an `invoke_vi` with `0xFFFFFFFF` (-1) hits — most likely a function pointer in the
  uiDC struct that was stored using a GOT.func value from instance 1 that in turn
  holds a stale or uninitialized reference.

The exact trigger is an `invoke_vi` call with index `0xFFFFFFFF` (syscall = -1
sentinel). This is consistent with the `-1` initializer in `ui_syscalls.c`:
```c
static intptr_t (QDECL *syscall)(intptr_t arg, ...) = (intptr_t(QDECL *)(intptr_t, ...)) - 1;
```

### Verification

Old `index.wasm` dylink.0:
```
NEEDED (2 libs):
  - 'cgame.mp.wasm'
  - 'ui.mp.wasm'
```

New `index.wasm` dylink.0 (after fix):
```
NEEDED: (section absent)
```

---

## The Fix

In `iortcw/Makefile`, the WASM `WASM_NATIVE_GAMECODE=1` block:

```makefile
# BEFORE (causes double-load):
CLIENT_LDFLAGS += -sMAIN_MODULE=1 \
  -sALLOW_TABLE_GROWTH=1 \
  $(B)/$(BASEGAME)/cgame.mp.$(SHLIBEXT) \
  $(B)/$(BASEGAME)/ui.mp.$(SHLIBEXT)

# AFTER (only game-code dlopen loads them, once, from WASM FS):
CLIENT_LDFLAGS += -sMAIN_MODULE=1 \
  -sALLOW_TABLE_GROWTH=1
```

With `MAIN_MODULE=1`, all engine functions are exported regardless of whether side
modules are in the link command — so removing them has no effect on symbol visibility
or the engine's function table completeness.

---

## Architecture Recap

```
index.wasm   — engine (MAIN_MODULE=1) — also imports __table_base (acts like side module)
cgame.mp.wasm — SIDE_MODULE=1 — 43 elem-seg functions, 193 GOT.func imports, ~600+ exports
ui.mp.wasm    — SIDE_MODULE=1 — 22 elem-seg functions, 164 GOT.func imports, 603 exports
```

Table layout after fix (one load each):
- Engine: indices 0..11876 (~11877 entries: 3463 elem-seg + ~8414 via addFunction)
- ui element segment: `__table_base = 11877`, fills 11877..11898 (22 slots)
- vmMain (func $4, NOT in element segment) → addFunction → index **11899** = 0x2E7B

---

## File Locations

- `iortcw/Makefile` lines ~1119-1128: WASM build flags (where the fix lives)
- `iortcw/code/qcommon/files.c` line 543: `FS_BuildWASMPath` (constructs `/main/X.wasm`)
- `mod/src/ui/ui_syscalls.c`: `syscall` initialized to -1 sentinel
- `mod/src/ui/ui_main.c` line 6159: `_UI_Init` — DC + uiDC setup

---

## Container Debug Commands

```bash
# Rebuild and redeploy after Makefile changes
cd /home/travis/s4ndmod26
docker compose build web && docker compose up -d web

# Verify neededDynlibs removed from new engine binary
docker cp s4ndmod26-web-1:/usr/share/nginx/html/play/index.wasm /tmp/check.wasm
python3 -c "
import struct
data = open('/tmp/check.wasm','rb').read()
# grep for 'cgame' in the binary — should not appear in dylink section
import re
if b'cgame.mp.wasm' in data[:5000]:
    print('FAIL: cgame still in neededDynlibs')
else:
    print('OK: cgame not in early binary data (no neededDynlibs)')
"

# Tail build log
docker compose build web 2>&1 | tee /tmp/wasm_build.log

# View nginx/container logs
docker logs s4ndmod26-web-1 --tail 100
```

---

## Historical Notes (prior session work)

### What was fixed in earlier sessions
- EM_ASM comma-splitting → switched to EM_JS
- Pak file downloads
- WebSocket port 27961→27960
- net_enabled=0 blocking networking
- MAIN_MODULE=2 table overflow → upgraded to MAIN_MODULE=1

### Key WAT Findings (ui.mp.wasm)
```
element segment:  $1 $17 $18...$34 $66 $229 $230 $231  (22 funcs)
vmMain = func $4  (NOT in element segment → added via addFunction → idx 11899)
dllEntry = func $522
__wasm_call_ctors (func $0): EMPTY
__wasm_apply_data_relocs (func $2): patches GOT.mem addresses only, no GOT.func
```

### Shared symbols between cgame.mp.wasm and ui.mp.wasm
Both export: `MenuParse_ownerdraw`, `ItemParse_ownerdraw`, `trap_R_DrawStretchPic`,
`trap_R_RenderScene`, and many trap_* syscall wrappers.

These shared exports are why `updateGOT`'s `replace=false` guard causes the
conflict — cgame's GOT.func values for these symbols block ui's values from
being set.
