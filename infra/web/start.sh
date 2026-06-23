#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
BASE="http://26.s4ndmod.com"

if command -v apt-get &>/dev/null; then
  sudo apt-get install -y libsdl2-2.0-0 libxss1 libxxf86vm1 libopenal1 libvorbisfile3 libcurl4 2>/dev/null || true
elif command -v dnf &>/dev/null; then
  sudo dnf install -y SDL2 libXScrnSaver libXxf86vm openal-soft libvorbis libcurl 2>/dev/null || true
elif command -v pacman &>/dev/null; then
  sudo pacman -S --noconfirm sdl2 libxss libxxf86vm openal libvorbis curl 2>/dev/null || true
fi

chmod +x "$DIR/iowolfmp.x86_64"
mkdir -p "$DIR/main"

echo "Checking base game paks..."
for pak in pak0 mp_pak0 mp_pak1 mp_pak2 mp_pak3 mp_pak4 mp_pak5; do
  if [ ! -f "$DIR/main/$pak.pk3" ]; then
    echo "  Downloading $pak.pk3..."
    curl -fsSL "$BASE/downloads/main/$pak.pk3" -o "$DIR/main/$pak.pk3"
  fi
done

exec env XMODIFIERS= GTK_IM_MODULE= QT_IM_MODULE= SDL_IM_MODULE=none SDL_VIDEODRIVER=x11 "$DIR/iowolfmp.x86_64" \
  +set fs_basepath "$DIR" \
  +set fs_homepath "$DIR" \
  +set fs_game s4ndmod26 \
  +connect rtcw.s4ndmod.com
