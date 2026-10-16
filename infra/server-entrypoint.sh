#!/bin/sh
set -e
umask 022
mkdir -p /rtcw/s4ndmod26/replays
chmod 755 /rtcw/s4ndmod26/replays

# Copy the game module and pk3 from the image's dist directory into the
# volume-mounted mod folder.  This ensures a docker pull always delivers the
# latest build without touching operator-managed files like server.cfg.
cp /rtcw/s4ndmod26-dist/qagame.mp.x86_64.so /rtcw/s4ndmod26/qagame.mp.x86_64.so
# The pk3 name carries the build version.  Remove every older copy (and the legacy
# unversioned name) first: with sv_pure on, two pk3s holding a cgame/ui would both be
# searched, and the stale one could be the one clients are told to use.
rm -f /rtcw/s4ndmod26/s4ndmod26.pk3 /rtcw/s4ndmod26/s4ndmod26_*.pk3
cp /rtcw/s4ndmod26-dist/s4ndmod26_*.pk3      /rtcw/s4ndmod26/
# The web container serves this folder as /downloads/s4ndmod26/; latest.txt names the current pk3.
basename /rtcw/s4ndmod26-dist/s4ndmod26_*.pk3 > /rtcw/s4ndmod26/latest.txt
# Retail game paks (pak0, mp_pak0-5, mp_pakmaps*) are never part of the image.  If the operator mounted them at
# /rtcw/retail/main, link them next to demopak0 so the server loads them: sv_pure then accepts clients that
# have either the demo or the retail files (the server only lists its own pk3s as required downloads).
find /rtcw/main -maxdepth 1 -type l -name '*.pk3' -lname '/rtcw/retail/*' -delete
for f in /rtcw/retail/main/*.pk3; do
    [ -f "$f" ] && ln -sf "$f" "/rtcw/main/$(basename "$f")"
done
exec /rtcw/iowolfded.x86_64 \
    +set dedicated 2 \
    +set fs_basepath /rtcw \
    +set fs_homepath /rtcw \
    +set fs_game s4ndmod26 \
    +set omnibot_path /rtcw/omni-bot \
    +exec server.cfg \
    "$@"
