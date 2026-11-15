#!/bin/sh
# Drops a client whose queue of unacknowledged server commands is full, on a real dedicated server, and counts
# how many times the game's disconnect hook runs.  Before the SV_DropClient fix (ioquake3 cc18246f) the drop's own
# "was kicked" print overflowed the queue and dropped the client a second time: two ClientDisconnect calls.
#
#   tools/local/test_dropclient.sh <iowolfded binary> <dir with main/ (retail paks) and s4ndmod26/qagame.mp.x86_64.so>
#
# The server needs `developer 1`, rcon password `testpw` and `sv_master1..5 ""` (the script sets the first two and
# the second directory's server.cfg should clear the masters).  Exits non-zero unless exactly one disconnect ran.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
bin=$1; base=$2
work=$(mktemp -d)
trap 'kill $pid 2>/dev/null || true; rm -rf "$work"' EXIT

max=$(sed -n 's/^#define[[:space:]]*MAX_RELIABLE_COMMANDS[[:space:]]*\([0-9]*\).*/\1/p' "$root/iortcw/code/qcommon/qcommon.h")
cp -r "$base/main" "$base/s4ndmod26" "$work/"
printf 'set sv_pure 0\nset rconpassword "testpw"\nset sv_master1 ""\nset sv_master2 ""\nset sv_master3 ""\nset sv_master4 ""\nset sv_master5 ""\n' > "$work/s4ndmod26/server.cfg"
i=0; : > "$work/s4ndmod26/flood.cfg"
while [ $i -lt "$max" ]; do echo "say m$i" >> "$work/s4ndmod26/flood.cfg"; i=$((i + 1)); done

q="$root/iortcw/code/qcommon"
${CC:-gcc} -O1 -DARCH_STRING='"x86_64"' -DPRODUCT_VERSION='"test"' -DBOTLIB -I"$q" -I"$root/iortcw/code/game" -I"$root/iortcw/code" \
	-o "$work/packets" "$root/tools/local/dropclient/packets.c" "$q/huffman.c" "$q/msg.c" "$q/q_shared.c" "$q/q_math.c" -lm

cd "$work"
"$bin" +set dedicated 2 +set developer 1 +set net_port 27977 +set fs_basepath "$work" +set fs_game s4ndmod26 +exec server.cfg +map mp_beach > server.log 2>&1 &
pid=$!
sleep 7
out=$(python3 "$root/tools/local/dropclient/silent_client.py" 27977 "$max" kick "$work/packets" testpw)
echo "$out"
echo "$out" | grep -q 'RESULT disconnect=1 zombie=1 overflow_list=0' || { echo "FAIL: the client was dropped more than once"; exit 1; }
echo "ok: one drop"
