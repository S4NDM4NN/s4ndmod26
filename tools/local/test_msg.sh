#!/bin/sh
# Tests the overflow detection of MSG_WriteBits (iortcw/code/qcommon/msg.c) under AddressSanitizer and UBSan.
# Needs gcc; takes a second.
#
#   tools/local/test_msg.sh
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
q="$root/iortcw/code/qcommon"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

${CC:-gcc} -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined -Wall \
	-DARCH_STRING='"x86_64"' -DPRODUCT_VERSION='"test"' -DBOTLIB -I"$q" -I"$root/iortcw/code/game" -I"$root/iortcw/code" \
	-o "$work/test_msg" "$root/tools/local/test_msg.c" "$q/msg.c" "$q/huffman.c" "$q/q_shared.c" "$q/q_math.c" -lm
"$work/test_msg"
