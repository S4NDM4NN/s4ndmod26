#!/bin/sh
# Tests Info_Validate in the game module's q_shared.c and the engine's.  Needs gcc; takes a second.
#
#   tools/local/test_infovalidate.sh
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cc="${CC:-gcc} -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined -Wall -DARCH_STRING=\"x86_64\" -DPRODUCT_VERSION=\"test\""

echo "== game module (mod/src/game/q_shared.c)"
$cc -I"$root/mod/src/game" -o "$work/t_game" "$root/tools/local/test_infovalidate.c" "$root/mod/src/game/q_shared.c" -lm 2>&1 | grep -E "error" && exit 1 || true
"$work/t_game"
echo "== engine (iortcw/code/qcommon/q_shared.c)"
$cc -DBOTLIB -I"$root/iortcw/code/qcommon" -I"$root/iortcw/code/game" -I"$root/iortcw/code" -o "$work/t_engine" "$root/tools/local/test_infovalidate.c" "$root/iortcw/code/qcommon/q_shared.c" "$root/iortcw/code/qcommon/q_math.c" -lm 2>&1 | grep -E "error" && exit 1 || true
"$work/t_engine"
