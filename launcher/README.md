# s4ndmod launcher

Keeps an S4NDMoD26 install current and starts the game.  Go, standard library only.

    s4ndmod-launcher.exe                 update if needed, then launch and connect
    s4ndmod-launcher.exe --check         show what would change, change nothing
    s4ndmod-launcher.exe --no-launch     update only
    s4ndmod-launcher.exe --dir D --server URL --connect HOST

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
