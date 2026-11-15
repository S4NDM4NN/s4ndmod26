# Next Session: WASM Multiplayer Debugging

## Session Update — 2026-06-29

This file started from an earlier state where the main theory was
"browser sends packets but never receives anything useful back."
That is no longer the current problem. The browser multiplayer path now
connects. The next debugging phase is the actual 3D world renderer.

### What is now working

- Browser WASM client does send and receive UDP traffic through the
  WebSocket bridge.
- The old stock `websockify` assumption was wrong for RTCW MP because the
  game server is UDP. Local dev now uses a custom WebSocket -> UDP bridge:
  `wasm/ws_udp_bridge.py`.
- WASM networking is no longer stuck behind `NET_Sleep()`. A browser-only
  socket poll path was added:
  - `iortcw/code/qcommon/qcommon.h`
  - `iortcw/code/qcommon/net_ip.c`
  - `iortcw/code/qcommon/common.c`
- The browser client now completes:
  - `getchallenge`
  - `connectResponse`
  - initial `gamestate`
- The client switches into `fs_game s4ndmod26`.
- Browser-side pak handling now works for MP joins instead of depending on
  the in-engine downloader.
- Chrome can connect and remain in-session against the local server when
  `sv_pure 0` is used during troubleshooting.

### Files changed in this session

- `wasm/shell.html`
  - `Module.websocket.url` routes browser sockets correctly.
- `wasm/ws_udp_bridge.py`
  - custom WS -> UDP bridge
  - handles Emscripten control frames
  - uses binary subprotocol
- `docker-compose.yml`
  - local `websockify` service now runs the Python UDP bridge
- `iortcw/code/qcommon/common.c`
  - browser path now polls sockets every frame
- `iortcw/code/qcommon/net_ip.c`
  - added `NET_Pump()`
  - narrowed noisy WASM packet logging
- `iortcw/code/qcommon/qcommon.h`
  - declaration for `NET_Pump()`
- `iortcw/code/client/cl_main.c`
  - browser-side pak download path
  - deferred cgame startup path for browser event-loop timing

### Stable commits from this session

- `e10e2bad` `Add browser-side WASM pak downloads`
- `0255927b` `Add WASM websocket bridge and socket polling`

### Important pk3 verification

There was a real pure-check mismatch during this session.

The pk3 embedded in the WASM bundle is inside `.wasm-out/index.data` as:

- `/s4ndmod26/s4ndmod26.pk3`

It was extracted and hashed. Matching hashes after resync:

- WASM bundled pk3: `2799b96dd5b513a47605fe75924dc975a920a8254d7ea4b0f10d8417bae80988`
- freshly built pk3: `2799b96dd5b513a47605fe75924dc975a920a8254d7ea4b0f10d8417bae80988`
- server-mounted `gamedata/s4ndmod26/s4ndmod26.pk3` after overwrite:
  `2799b96dd5b513a47605fe75924dc975a920a8254d7ea4b0f10d8417bae80988`

So:

- yes, there was a stale/different server pk3 at one point
- no, the current blocker is not "WASM is loading an old cgame binary from the pk3"

The WASM client loads these directly, not from the pk3:

- `/main/ui.mp.wasm`
- `/main/cgame.mp.wasm`

The native `.so` / `.dll` files inside `s4ndmod26.pk3` are for Linux/Windows
clients, not the browser VM loader.

### Current blocker

The active blocker is no longer connect/load sequencing. That path was
good enough to enter a live multiplayer session.

The current failure is the in-game 3D render output:

- audio proves the client is connected
- snapshots advance and spectating works
- something from the scene is moving on-screen
- the world image is extremely dark / indistinct / badly composed
- stale text or old pixels can remain visible, which suggests incomplete
  clears or bad viewport/state transitions are still possible symptoms

This now looks like a renderer correctness problem in the shared 3D path,
not just a menu/UI issue and not just a networking issue.

### What we already ruled out

- not just "wrong resolution"
- not just `sv_pure`
- not just "could not download required paks"
- not fixed by forcing vertex lighting / bypassing lightmaps alone
- not fixed by the earlier viewport/scissor reset experiments alone

### Best current renderer theory

The browser build is reaching the world render path, but some shared GL /
fixed-function state is still wrong once the real 3D scene starts.

Most plausible buckets now:

- world draw surfaces are submitted, but a texture coord / texenv /
  multitexture state mismatch makes the result nearly black
- depth, clear, or viewport/scissor state is not fully restored between
  passes, so old text/UI fragments remain on-screen
- the world view is being rendered into the wrong effective sub-rectangle,
  making the scene appear tiny, offset, or smeared into a smaller area
- a gamma / overbright / lightmap combine path in WASM/gl4es is wrong for
  real world surfaces even though some 2D and menu-model rendering works

### Current relevant log facts

- Browser can connect and stay connected in multiplayer
- Browser can spectate a bot
- Connection/audio state is real; this is not a fake half-connected UI
- The earlier `CL_GetServerCommand` handoff failure is no longer the main
  blocker for the current local setup

### Useful commands used in this session

- `make wasm-iterate`
- `docker compose up -d --force-recreate websockify`
- `docker compose restart rtcw-server`
- `docker compose logs --no-color --tail=... rtcw-server`

### Important note for next session

Do not restart from the old theories:

- "websocket URL / UDP bridge / no inbound packets"
- "the first blocker is CL_GetServerCommand sequencing"
- "this is probably just the old menu/UI viewport bug"

Those were real issues at different points, but the next useful work is in
the actual multiplayer world renderer.

### Next investigation order

1. Compare menu rendering path vs real world rendering path.
   - Verify whether `RDF_NOWORLDMODEL` scenes are mostly acceptable now while
     normal world scenes remain wrong.
   - If true, focus on shared world-surface submission rather than menu-only
     composition code.
2. Instrument the first real in-game world frame.
   - Log one-shot values for:
     - `glConfig.vidWidth/vidHeight`
     - `refdef.x/y/width/height`
     - final `GL_VIEWPORT`
     - final `GL_SCISSOR_BOX`
     - clear bits used in `RB_BeginDrawingView()`
     - whether `RDF_NOWORLDMODEL` is off for the bad frame
3. Measure whether world geometry is actually being submitted.
   - Add low-volume counters around world-surface paths to confirm:
     - nonzero draw surfaces
     - nonzero world batches
     - whether the bad frame is mostly lightmapped stages, unlit stages, or
       something else
4. Validate clear/depth behavior.
   - The user report about text artifacts staying on-screen makes this a high
     value check.
   - Confirm color/depth clears for the first bad multiplayer frame.
5. Re-check lightmap / combine assumptions only after the above.
   - Earlier forced-lighting experiments were useful, but they did not solve
     the world image by themselves.

### Recommended first files for the renderer phase

- `iortcw/code/renderer/tr_backend.c`
- `iortcw/code/renderer/tr_shade.c`
- `iortcw/code/renderer/tr_world.c`
- `iortcw/code/renderer/tr_bsp.c`
- `iortcw/code/renderer/tr_main.c`

## Current State

Historical note: the sections below describe the earlier networking bring-up
and are now mostly background context rather than the active blocker.

The WASM client (`/play`) loads and reaches the main menu as of commit `59f859ee`.
The game server runs in Docker/K8s alongside a websockify proxy that bridges
WebSocket (TCP) → UDP to the game server.

Connecting to the server fails: the client opens a socket, sends the initial
packet, then waits forever with no response.

---

## Architecture

```
Browser (HTTPS)
  └─ WASM game (Emscripten SOCKFS)
       └─ WebSocket connect() call
            └─ ws://[server-ip]:27960   ← THIS IS WRONG (see Problem 1)
                 └─ websockify (TCP 27960)
                      └─ rtcw-server:27960/UDP
```

Correct path for K8s (dev-alpha):
```
wss://s4ndmod26.dev-alpha.s4ndmod.com/ws  →  websockify ClusterIP  →  rtcw-server:27960/UDP
```

---

## Problem 1 — Wrong WebSocket URL (must fix first)

Emscripten's SOCKFS constructs the WebSocket URL from the destination address
passed to `sendto()`. When the user connects to server IP `1.2.3.4:27960`,
SOCKFS opens `ws://1.2.3.4:27960`. This breaks in two ways:

1. **Mixed content**: the game is served from HTTPS; browsers block `ws://`
   connections from HTTPS pages. Must be `wss://`.
2. **Wrong target**: in K8s, websockify is behind Traefik at `/ws`, not
   directly reachable on port 27960 from the public internet.

### Fix

Override `Module.websocket` in `wasm/shell.html` (or `wasm/index.html`)
**before** the Emscripten module initializes:

```html
<script>
  var Module = {
    websocket: {
      // Route all game sockets through the Traefik-terminated WSS path.
      // In local dev (http) fall back to raw ws:// on port 27960.
      url: (function() {
        if (window.location.protocol === 'https:') {
          return 'wss://' + window.location.hostname + '/ws';
        }
        // Dev: ws://localhost:27960 (websockify exposed by docker-compose)
        return 'ws://' + window.location.hostname + ':27960';
      })()
    }
  };
</script>
```

Emscripten SOCKFS respects `Module.websocket.url` as the WebSocket endpoint
regardless of the `connect()` destination address. All game traffic then flows
through `wss://[hostname]/ws` → Traefik → websockify ClusterIP → UDP.

File to edit: `wasm/shell.html` — find the existing `<script>` block near the
top and add the `Module.websocket` assignment before the `{{{ SCRIPT }}}` tag.
Make sure it appears before Emscripten's generated script loads.

---

## Problem 2 — recvfrom() address mismatch

Even after Problem 1 is fixed, received packets may be silently dropped.

RTCW validates that every incoming UDP packet's source address matches the
server address the client connected to. With Emscripten SOCKFS + WebSocket,
the `from` address returned by `recvfrom()` is whatever SOCKFS reports for the
WebSocket peer — typically the websockify proxy address, not the game server's
real IP.

### Where to look

- `iortcw/code/qcommon/net_ip.c` — `NET_GetPacket()`, the `recvfrom()` call
  and the `NET_SockaddrToAdr()` conversion that populates `net_from`.
- `iortcw/code/client/cl_main.c` — `CL_PacketEvent()` / `CL_CheckPackets()`
  where the source address is validated against `clc.serverAddress`.

### Likely fix options

**Option A — skip address check in WASM**
Add an `#ifdef __EMSCRIPTEN__` guard in `CL_PacketEvent` (or wherever the
source address is compared) to accept packets from any address when running
in the browser. This is safe because the browser's same-origin policy already
restricts which WebSocket servers can be reached.

**Option B — spoof the from address**
In `NET_GetPacket`, for `__EMSCRIPTEN__`, replace the `from` address returned
by `recvfrom()` with the address of the connected server (`clc.serverAddress`
or stored in a global set at connect time). This makes the rest of the engine
see the correct server IP.

Option B is cleaner (no changes outside the network layer) but requires storing
the connected server address. Option A is quicker to test.

---

## Problem 3 — Non-blocking recvfrom / event loop interaction

Emscripten SOCKFS uses asynchronous WebSocket events under the hood but
`recvfrom()` is called synchronously in the game's frame loop. Data arrives
in a WebSocket `onmessage` callback and is queued; `recvfrom()` dequeues it.

This should work out of the box with SOCKFS, but if packets are being lost or
the game shows "Connection interrupted" / times out quickly, check:

- Whether `EAGAIN` is returned correctly when no data is queued (expected:
  game ignores it and retries next frame).
- Whether the WebSocket connection is actually being established at all —
  add a `console.log` in the SOCKFS `connect` path or watch the Network tab
  in DevTools for a WebSocket entry.

---

## Problem 4 — bind() with PORT_ANY

`NET_OpenIP` (in `net_ip.c`, EMSCRIPTEN path) calls
`NET_IPSocket(net_ip, PORT_ANY, &err)` which calls
`bind(sock, 0.0.0.0:0, ...)`. SOCKFS's `bind` for UDP is a no-op (records
the address without creating a real socket). This is fine; just confirming it
doesn't interfere.

The `ip_socket` that results from this bind is what `NET_GetPacket` and
`NET_SendPacket` use. As long as SOCKFS routes all traffic through the one
WebSocket connection opened by the first `sendto()`, the bind port is
irrelevant.

---

## Suggested Debug Sequence

1. **Apply the Module.websocket.url fix** (Problem 1).
2. Build and deploy locally: `docker compose up --build`.
3. Open `/play` in Chrome with DevTools → Network tab → WS filter.
4. Connect to the server in-game.
5. Verify a WebSocket connection appears in DevTools targeting `ws://localhost:27960`.
6. Check if frames are being sent/received in the WebSocket inspector.
7. If frames are sent but game times out → address mismatch (Problem 2).
8. If no WebSocket connection appears → `Module.websocket` not being read by
   Emscripten — check script load order in shell.html.

---

## Relevant Files

| File | Why |
|------|-----|
| `wasm/shell.html` | Add `Module.websocket.url` override |
| `iortcw/code/qcommon/net_ip.c` | `NET_IPSocket`, `NET_GetPacket`, `NET_OpenIP` |
| `iortcw/code/client/cl_main.c` | Packet source address validation |
| `docker-compose.yml` | websockify container (reference impl) |
| `~/gitops/apps/s4ndmod26/base/deployment-websockify.yaml` | K8s websockify |
| `~/gitops/apps/s4ndmod26/base/ingress.yaml` | `/ws` → websockify route |

---

## K8s Deploy Notes

The gitops repo (`~/gitops`) already has websockify manifests committed
(`d9d1196`). After the code fixes are working locally, update the dev overlay
image tags in `apps/s4ndmod26/overlays/dev/kustomization.yaml` to pick up
the new web image (which will contain the `Module.websocket.url` fix).

The `dev-alpha` cluster ingress routes `wss://s4ndmod26.dev-alpha.s4ndmod.com/ws`
through Traefik to the websockify ClusterIP service. No additional K8s changes
should be needed once the WASM shell is patched.
