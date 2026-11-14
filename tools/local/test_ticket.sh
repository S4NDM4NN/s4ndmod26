#!/bin/sh
# Tests the game module's join ticket code (mod/src/game/bg_ed25519.c, g_ticket_core.c) against the Go code that
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
go run ./cmd/ticketvec sign > "$work/sign.txt"
go run ./cmd/ticketvec auth > "$work/auth.txt"
go run ./cmd/ticketvec tickets > "$work/tickets.txt"
go run ./cmd/ticketvec link > "$work/link.txt"

cc="${CC:-gcc} -O1 -g -fsanitize=address,undefined -fno-sanitize-recover=undefined -Wall -Wextra -I$game"
$cc -o "$work/test_ed25519" "$root/tools/local/test_ed25519.c" "$game/bg_ed25519.c"
$cc -o "$work/test_ed25519_sign" "$root/tools/local/test_ed25519_sign.c" "$game/bg_ed25519.c"
$cc -o "$work/test_auth" "$root/tools/local/test_auth.c" "$game/g_auth_core.c" "$game/bg_identity.c" "$game/g_ticket_core.c" "$game/bg_ed25519.c"
$cc -o "$work/test_link" "$root/tools/local/test_link.c" "$game/bg_identity.c" "$game/bg_ed25519.c"
$cc -o "$work/test_ticket" "$root/tools/local/test_ticket.c" "$game/g_ticket_core.c" "$game/bg_ed25519.c"

echo "== Ed25519 verifying (C against Go)"
"$work/test_ed25519" < "$work/ed.txt"
echo "== Ed25519 key derivation and signing (C against Go, exact bytes)"
"$work/test_ed25519_sign" < "$work/sign.txt"
echo "== tickets (C against Go), decoders, single-use table"
"$work/test_ticket" < "$work/tickets.txt"
echo "== player identity check (C against Go), the client's side, nonce"
"$work/test_auth" < "$work/auth.txt"
echo "== the automatic account link: the bytes the key signs (C against Go)"
"$work/test_link" < "$work/link.txt"
