#!/bin/sh
set -e
umask 022
mkdir -p /rtcw/s4ndmod26/replays
chmod 755 /rtcw/s4ndmod26/replays

# Copy the game module and pk3 from the image's dist directory into the
# volume-mounted mod folder.  This ensures a docker pull always delivers the
# latest build without touching operator-managed files like server.cfg.
cp /rtcw/s4ndmod26-dist/qagame.mp.x86_64.so /rtcw/s4ndmod26/qagame.mp.x86_64.so
cp /rtcw/s4ndmod26-dist/s4ndmod26.pk3        /rtcw/s4ndmod26/s4ndmod26.pk3
exec /rtcw/iowolfded.x86_64 \
    +set dedicated 2 \
    +set fs_basepath /rtcw \
    +set fs_homepath /rtcw \
    +set fs_game s4ndmod26 \
    +set omnibot_path /rtcw/omni-bot \
    +exec server.cfg \
    "$@"
