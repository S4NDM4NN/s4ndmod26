#!/bin/bash
# Mirror of the Dockerfile iortcw-wasm-builder stage, in a scratch dir.
set -e
R="$(cd "$(dirname "$0")/../.." && pwd)"
# S = work dir (build tree, logs, output).  gl4es must be built once into $S/gl4es-opt (see AGENTS.md).
S="${WASM_WORK:-$HOME/.cache/s4ndmod26-wasm}"
mkdir -p "$S"
W=$S/wb2
source "${EMSDK_ENV:-$HOME/.local/share/emsdk/emsdk_env.sh}" >/dev/null 2>&1
mkdir -p $W/third_party
# sync sources (keep build dir for incremental builds)
mkdir -p $W/iortcw
cp -ru $R/iortcw/. $W/iortcw/ 2>/dev/null || true
rm -rf $W/iortcw/wasm && cp -r $R/wasm $W/iortcw/wasm
cp -r $R/third_party/zlib $W/third_party/ 2>/dev/null || true
sync_dir() { for f in "$1"/*; do [ -f "$f" ] || continue; cmp -s "$f" "$2/$(basename $f)" || cp --remove-destination "$f" "$2/"; done; }
sync_dir $R/mod/src/cgame $W/iortcw/code/cgame
sync_dir $R/mod/src/ui $W/iortcw/code/ui
sync_dir $R/mod/src/game $W/iortcw/code/game
mkdir -p $W/iortcw/main/ui_mp && cp $R/mod/main/ui_mp/menudef.h $W/iortcw/main/ui_mp/
cd $W/iortcw
[ -f code/game/g_version.h ] || printf '#pragma once\n#define MOD_BUILD_VERSION "S4NDMoD local"\n' > code/game/g_version.h
for f in adler32 compress crc32 deflate trees zutil uncompr inflate inffast inftrees; do cmp -s $W/third_party/zlib/$f.c code/game/zlib_$f.c || cp $W/third_party/zlib/$f.c code/game/zlib_$f.c; done
for h in $W/third_party/zlib/*.h; do cmp -s $h code/game/$(basename $h) || cp $h code/game/; done
# minimal mod pk3 (content only, no native libs)
mkdir -p wasm/fs/main wasm/fs/s4ndmod26
python3 - <<PY
import zipfile,os
R="$R"
out="$W/iortcw/wasm/fs/s4ndmod26/s4ndmod26.pk3"
z=zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED)
src=zipfile.ZipFile(R+"/assets/game/ob_media.pk3")
for n in src.namelist():
    if not n.endswith('/'): z.writestr(n,src.read(n))
for root,_,files in os.walk(R+"/mod/main/ui_mp"):
    for f in files:
        p=os.path.join(root,f); z.write(p,"ui_mp/"+os.path.relpath(p,R+"/mod/main/ui_mp"))
for root,_,files in os.walk(R+"/mod/main/gfx"):
    for f in files:
        p=os.path.join(root,f); z.write(p,"gfx/"+os.path.relpath(p,R+"/mod/main/gfx"))
z.write(R+"/mod/main/controller.cfg","controller.cfg")
z.close()
PY
COMMON="GL4ES_PATH=$S/gl4es-opt BUILD_STANDALONE=1 WASM_NATIVE_GAMECODE=1 TOOLS_CC=gcc"
EMSCRIPTEN=1 emmake make -j16 $COMMON BUILD_CLIENT=0 BUILD_SERVER=0 BUILD_GAME_SO=1 BUILD_GAME_QVM=0 BUILD_RENDERER_OPENGL1=0 BUILD_RENDERER_OPENGL2=0 WASM_S4ND_QAGAME=1 release > $S/wasm_pass1.log 2>&1
cp build/release-emscripten-wasm/main/cgame.mp.wasm build/release-emscripten-wasm/main/ui.mp.wasm build/release-emscripten-wasm/main/qagame.mp.wasm wasm/fs/main/
rm -f build/release-emscripten-wasm/index.html build/release-emscripten-wasm/index.data build/release-emscripten-wasm/index.js build/release-emscripten-wasm/index.wasm build/release-emscripten-wasm/index.worker.js build/release-emscripten-wasm.zip
EMSCRIPTEN=1 emmake make -j16 $COMMON VERSION=S4NDMoD_local BUILD_CLIENT=1 BUILD_SERVER=0 BUILD_GAME_SO=0 BUILD_GAME_QVM=0 BUILD_RENDERER_OPENGL1=1 BUILD_RENDERER_OPENGL2=0 release > $S/wasm_pass2.log 2>&1
mkdir -p $S/play && cp build/release-emscripten-wasm/index.* $S/play/ && cp build/release-emscripten-wasm/main/cgame.mp.wasm build/release-emscripten-wasm/main/ui.mp.wasm $S/play/
cp "$R"/infra/web/nginx/html/play/*.js $S/play/
ls -la $S/play
