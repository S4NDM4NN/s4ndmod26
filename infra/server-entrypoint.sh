#!/bin/sh
set -e
umask 022
mkdir -p /rtcw/s4ndmod26/replays
chmod 755 /rtcw/s4ndmod26/replays
exec /rtcw/iowolfded.x86_64 \
    +set dedicated 2 \
    +set fs_basepath /rtcw \
    +set fs_homepath /rtcw \
    +set fs_game s4ndmod26 \
    +set omnibot_path /rtcw/omni-bot \
    +exec server.cfg \
    "$@"
