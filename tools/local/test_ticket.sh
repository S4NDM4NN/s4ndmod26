#!/bin/sh
# Tests the game module's join ticket code (mod/src/game/g_ed25519.c, g_ticket_core.c) against the Go code that
# signs the tickets (infra/web/status-api/ticket): thousands of vectors made with Go's crypto/ed25519 and
# ticket.Issue, including every single-bit change of a signature, message, key and ticket.  The C is built with
# AddressSanitizer and UBSan, and any finding is a failure.  Needs go and gcc; takes a few seconds.
#
#   tools/local/test_ticket.sh
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
game="$root/mod/src/game"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cd "$root/infra/web/status-api"
go run ./cmd/ticketvec ed25519 > "$work/ed.txt"
go run ./cmd/ticketvec tickets > "$work/tickets.txt"

cc="${CC:-gcc} -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined -Wall -Wextra -I$game"
$cc -o "$work/test_ed25519" "$root/tools/local/test_ed25519.c" "$game/g_ed25519.c"
$cc -o "$work/test_ticket" "$root/tools/local/test_ticket.c" "$game/g_ticket_core.c" "$game/g_ed25519.c"

echo "== Ed25519 (C against Go)"
"$work/test_ed25519" < "$work/ed.txt"
echo "== tickets (C against Go), decoders, single-use table"
"$work/test_ticket" < "$work/tickets.txt"
