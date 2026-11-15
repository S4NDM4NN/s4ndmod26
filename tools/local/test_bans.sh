#!/bin/sh
# Checks that `rehashbans` in server.cfg (run before the first map, so before the server is "running") loads the
# saved bans: ban an address, restart, and list the bans.  Before the fix (ioquake3 56f9f94e) the command did
# nothing until the server was running, so a restart forgot every ban until someone ran it by hand.
#
#   tools/local/test_bans.sh <iowolfded binary> <dir with main/ (retail paks) and s4ndmod26/qagame.mp.x86_64.so>
set -eu
bin=$1; base=$2
work=$(mktemp -d)
pid=
trap '[ -z "$pid" ] || kill $pid 2>/dev/null || true; rm -rf "$work"' EXIT
cp -r "$base/main" "$base/s4ndmod26" "$work/"
mkdir "$work/home"
printf 'set sv_pure 0\nset rconpassword "testpw"\nset sv_master1 ""\nset sv_master2 ""\nset sv_master3 ""\nset sv_master4 ""\nset sv_master5 ""\n' > "$work/s4ndmod26/server.cfg"

rcon() {
	python3 - "$1" <<'PY'
import socket, sys
t = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
t.settimeout(2)
t.sendto(b"\xff\xff\xff\xffrcon testpw " + sys.argv[1].encode(), ("127.0.0.1", 27978))
try:
    print(t.recvfrom(4096)[0][4:].decode().replace("print\n", "").strip())
except OSError:
    print("(no reply)")
PY
}
start() {
	"$bin" +set dedicated 2 +set net_port 27978 +set fs_basepath "$work" +set fs_homepath "$work/home" +set fs_game s4ndmod26 \
		+exec server.cfg +map mp_beach > "$work/$1.log" 2>&1 &
	pid=$!
	sleep 7
}
stop() { kill $pid; wait $pid 2>/dev/null || true; pid=; }

start first
rcon "banaddr 10.9.8.7" > /dev/null
sleep 1
stop
[ -s "$work/home/s4ndmod26/serverbans.dat" ] || { echo "FAIL: no ban file was written"; exit 1; }

printf 'rehashbans\n' >> "$work/s4ndmod26/server.cfg"
start second
out=$(rcon listbans)
stop
echo "listbans after a restart with rehashbans in server.cfg: $out"
echo "$out" | grep -q "10.9.8.7" || { echo "FAIL: the saved ban was not loaded"; exit 1; }
echo "ok: bans loaded at startup"
