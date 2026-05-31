#!/usr/bin/env bash
set -euo pipefail

BASE="${RTCW_BASE:-http://s4ndmod.com}"
DEST="${RTCW_DIR:-$HOME/rtcw}"

echo "Installing iortcw + s4ndmod26 to $DEST"
mkdir -p "$DEST/main" "$DEST/s4ndmod26"

dl() { echo "  $2"; curl -fsSL "$1" -o "$2"; }

cd "$DEST"
dl "$BASE/downloads/linux/iowolfmp.x86_64"            iowolfmp.x86_64
dl "$BASE/downloads/linux/renderer_mp_opengl1_x86_64.so" renderer_mp_opengl1_x86_64.so
chmod +x iowolfmp.x86_64

echo "Downloading base paks..."
for pk in pak0 mp_pak0 mp_pak1 mp_pak2 mp_pak3 mp_pak4 mp_pak5; do
  dl "$BASE/downloads/main/$pk.pk3" "main/$pk.pk3"
done

dl "$BASE/downloads/s4ndmod26.pk3" "s4ndmod26/s4ndmod26.pk3"

echo ""
echo "Done. Run the game with:"
echo "  $DEST/iowolfmp.x86_64 +set fs_basepath $DEST +connect s4ndmod.com"
