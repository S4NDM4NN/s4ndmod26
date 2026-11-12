#!/bin/sh
# Prints the game version: v<yy.mm.dd>-<sha8> of the last commit that changed anything the game build uses
# (mod, engine, bots, assets, browser client, launcher, server image).  A commit that only changes the website
# leaves it as it was, so the server image, the pk3 name and the client downloads stay the same and the game
# server is not restarted.
#
# Keep GAME_PATHS in step with what the game stages of the Dockerfile COPY.  The Dockerfile itself is listed
# because it defines those stages; an edit to its web stage alone therefore rebuilds the game too (harmless, the
# layer cache keeps it quick).
#
# Needs the full history (actions/checkout with fetch-depth: 0).
set -eu
cd "$(git rev-parse --show-toplevel)"

GAME_PATHS="mod iortcw omnibot assets wasm launcher third_party
  gamedata/s4ndmod26/server.cfg
  infra/server-entrypoint.sh infra/web/start.sh infra/web/start.ps1
  Dockerfile Dockerfile.build-base .dockerignore .gitmodules"

# shellcheck disable=SC2086
sha=$(git log -1 --format=%H HEAD -- $GAME_PATHS)
[ -n "$sha" ] || { echo "game-version: no commit touches the game paths" >&2; exit 1; }
date=$(git log -1 --format=%cd --date=format:%y.%m.%d "$sha")
echo "v${date}-$(echo "$sha" | cut -c1-8)"
