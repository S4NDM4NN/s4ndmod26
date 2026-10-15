# s4ndmod launcher

Keeps an S4NDMoD26 install current and starts the game.  Go, standard library only.

    s4ndmod-launcher.exe                 window: updates if needed, then PLAY
    s4ndmod-launcher.exe --cli           the same in the console
    s4ndmod-launcher.exe --check         show what would change, change nothing
    s4ndmod-launcher.exe --no-launch     update only
    s4ndmod-launcher.exe --dir D --server URL --connect HOST

The window is a small HTML page (`ui/index.html`, embedded) served from a loopback-only HTTP server
inside the exe and shown in a native WebView2 window on Windows (no CGO, so it cross-compiles from
Docker; Windows 10/11 ship the WebView2 runtime).  Where WebView2 or a native window is not available
(Linux, or a Windows without the runtime) it opens the page in the default browser instead and quits
when that tab is closed.  Every request needs a random per-run token and a loopback Host header.
Server, install folder and connect host are editable under Settings and saved to
`<user config dir>/s4ndmod-launcher/settings.json`.

What it does each run:

1. `GET <server>/api/manifest?platform=windows|linux` (built by `infra/web/status-api/manifest.go`
   from the files actually being served: client binaries, the current `s4ndmod26_<version>.pk3`,
   `main/demopak0.pk3`; each with size and sha256).
2. Downloads only files that are missing or whose size/hash differ, to `<file>.part`, verifies, then
   renames into place.
3. Removes what the manifest's `prune` globs match and the manifest does not list: older mod pk3s,
   `name.<crc>.pk3` duplicates, and loose `cgame`/`ui` modules (an unpure server loads those ahead
   of the pk3, which is how a stale one silently wins).
4. Starts the game with `fs_basepath` = `fs_homepath` = the install dir, so nothing is read from or
   written to anywhere else.

The default install dir on Windows is `Documents\RTCW` (where the engine already keeps configs, so
existing binds carry over); on Linux `~/rtcw`.

If the server cannot be reached and a game is already installed, it launches the installed copy.
Manifest paths and prune patterns are rejected unless they stay inside the install dir.

Build/test: `go vet ./... && go test ./...`; the Dockerfile `launcher-builder` stage cross-compiles
`s4ndmod-launcher.exe` and `s4ndmod-launcher-linux` and the web image serves them at
`/downloads/launcher/`.

## Self-update

The manifest also carries the sha256 of the launcher binary the server publishes
(`/downloads/launcher/`).  If that differs from the running executable, the launcher downloads the new
one next to itself (`<exe>.new`), verifies size and sha256, renames the running exe to `<exe>.old`
(allowed on Windows, unlike overwriting it), moves the new one into place, and starts it with the same
arguments plus `--updated`.  The next run deletes `<exe>.old`.

- There is no version comparison, only "same bytes as the server": a server-side rollback downgrades
  clients as well.
- `--updated` (set on the relaunched process) and a `dev` build skip the check, so a hash that can never
  match cannot loop; `--no-self-update` turns it off.
- Any failure (download, hash, rename, start) leaves the original launcher in place and runnable; the
  game update carries on regardless.

### Signed updates

A launcher only installs an update that carries an ed25519 signature (over the new binary's sha256)
from a key embedded in it (`signing_keys.txt`).  HTTPS plus the manifest hash only says "this is what
the server serves"; the signature means a compromised web server or a tampered download cannot push
code to players, because the private key exists only in CI.

- `cmd/keygen` makes a keypair (private key to a 0600 file, public key printed);
  `cmd/sign -key FILE binary...` writes `<binary>.sig`.
- CI (push to main only, never PRs) writes the `LAUNCHER_SIGNING_KEY` Gitea secret to a file and the
  Dockerfile's `launcher-builder` stage signs with it via a BuildKit secret (never a layer or arg).
  The web image publishes `<binary>.sig` next to the binaries and `/api/manifest` returns it as
  `launcher.signature`.
- No key / no `.sig` (PR previews, local builds): the build is simply never offered as an update.
- A bad or missing signature is rejected before anything is downloaded, and the original launcher is
  left untouched.
- Rotation: add the new public key as another line in `signing_keys.txt` and ship that launcher while
  the old key still signs; remove the old key in a later release.  Losing the private key without
  having shipped a second key strands existing launchers (they need one manual download).
- This does not make Windows trust the exe (SmartScreen/Smart App Control look at Authenticode, which
  needs a purchased certificate).  The PowerShell installer and the self-update path download without
  the browser's "from the internet" mark, so they are not subject to the SmartScreen prompt.
