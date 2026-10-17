#!/bin/bash
# Build and run the standalone per-weapon stats test (tools/local/test_wstats.c).  No game, no engine needed.
set -e
R="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$(mktemp -d)"
gcc -Wall -Wextra -Wno-unused-parameter -DGAMEDLL -I "$R/mod/src/game" -o "$OUT/wstats_test" "$R/tools/local/test_wstats.c" "$R/mod/src/game/bg_wstats.c"
"$OUT/wstats_test"
rm -rf "$OUT"
