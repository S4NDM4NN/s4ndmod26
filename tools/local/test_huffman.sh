#!/bin/sh
# Tests Huff_Decompress (iortcw/code/qcommon/huffman.c) against malformed out-of-band packets, built with
# AddressSanitizer and UBSan (any finding fails), and checks valid text still round-trips.  Needs gcc; takes a
# second.
#
#   tools/local/test_huffman.sh
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
q="$root/iortcw/code/qcommon"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

${CC:-gcc} -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined -Wall \
	-DARCH_STRING='"x86_64"' -DPRODUCT_VERSION='"test"' -DBOTLIB -I"$q" -I"$root/iortcw/code/game" -I"$root/iortcw/code" \
	-o "$work/test_huffman" "$root/tools/local/test_huffman.c" "$q/huffman.c" 2>&1 | grep -v "^$" | grep -E "error" && exit 1 || true
"$work/test_huffman"
