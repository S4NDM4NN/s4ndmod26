#!/usr/bin/env python3
"""A client that connects to a local server and then says nothing: it never acknowledges a server command.

    silent_client.py <udp port> <number of say lines> <kick|nokick> <packets tool> <rcon password>

It connects (challenge, the Huffman-coded connect packet), sends one client packet so the server sends the
gamestate (state CS_PRIMED), then uses rcon `exec flood.cfg` (the caller writes <count> `say` lines into the
server's mod folder) so the server queues that many reliable commands for it.  With `kick` it then kicks the
client and prints what the server logged while dropping it.  Counts of the disconnect hook are on the last line."""
import socket, subprocess, sys, time, struct

port, nsay, kick, tool, pw = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3] == "kick", sys.argv[4], sys.argv[5]
a = ("127.0.0.1", port)
QPORT = 4242
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(2)


def rcon(cmd):
    # a new socket for each command: the server rate-limits rcon per source
    t = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    t.settimeout(2)
    t.sendto(b"\xff\xff\xff\xffrcon " + pw.encode() + b" " + cmd.encode(), a)
    try:
        out = [t.recvfrom(8192)[0][4:]]
    except OSError:
        return None
    t.settimeout(0.7)
    try:
        while True:
            out.append(t.recvfrom(8192)[0][4:].replace(b"print\n", b"", 1))
    except OSError:
        pass
    return b"".join(out).decode(errors="replace")


s.sendto(b"\xff\xff\xff\xffgetchallenge", a)
ch = s.recvfrom(4096)[0].split()[1].decode()
userinfo = r"\challenge\%s\protocol\61\qport\%d\name\Silent\rate\25000\snaps\20" % (ch, QPORT)
s.sendto(subprocess.check_output([tool, "connect", userinfo]), a)
resp = s.recvfrom(4096)[0]
if b"connectResponse" not in resp:
    sys.exit("connect refused: %r" % resp[:80])
challenge = int(resp.split()[1])

# One client packet whose server id is not the server's makes it send the gamestate (CS_CONNECTED -> CS_PRIMED).
# Netchan header: sequence, qport, checksum (challenge ^ sequence * challenge).
seq = 1
chk = (challenge ^ (seq * challenge)) & 0xffffffff
body = subprocess.check_output([tool, "msg", "0", "0", "0"])
s.sendto(struct.pack("<IHI", seq, QPORT, chk) + body, a)
t0 = time.time()
s.settimeout(0.5)
try:
    while time.time() - t0 < 1.5:
        s.recvfrom(8192)
except OSError:
    pass

rcon("exec flood.cfg")  # nsay `say` lines: each is one reliable command for every client
if kick:
    time.sleep(2)
    for _ in range(8):
        r = rcon("clientkick 0")
        if r and "kicked" in r:
            print("\n".join(l for l in r.splitlines() if not l.startswith("cmd ")))
            print("RESULT disconnect=%d zombie=%d overflow_list=%d" % (
                r.count("ClientDisconnect: 0"), r.count("Going to CS_ZOMBIE"), r.count("pending server commands")))
            sys.exit(0)
        time.sleep(1.2)
    sys.exit("the kick never went through")
