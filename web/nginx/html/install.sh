#!/usr/bin/env bash
set -euo pipefail

BASE="${RTCW_BASE:-http://s4ndmod.com}"
DEST="${RTCW_DIR:-$HOME/rtcw}"

# Install runtime dependencies
if command -v dnf &>/dev/null; then
  echo "Installing dependencies (dnf)..."
  sudo dnf install -y SDL2 libXScrnSaver libXxf86vm openal-soft libvorbis libcurl 2>/dev/null || true
elif command -v apt-get &>/dev/null; then
  echo "Installing dependencies (apt)..."
  sudo apt-get install -y libsdl2-2.0-0 libxss1 libxxf86vm1 libopenal1 libvorbisfile3 libcurl4 2>/dev/null || true
elif command -v pacman &>/dev/null; then
  echo "Installing dependencies (pacman)..."
  sudo pacman -S --noconfirm sdl2 libxss libxxf86vm openal libvorbis curl 2>/dev/null || true
fi

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
