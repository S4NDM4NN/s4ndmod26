#!/usr/bin/env bash
# Installs the S4NDMoD26 launcher (Linux x86_64).  The launcher then installs and updates the game itself.
#   curl -sL https://host/install.sh | RTCW_BASE="https://host" bash
# Optional: S4ND_DIR to choose where the launcher goes (default ~/.local/share/s4ndmod26).
set -euo pipefail

BASE="${RTCW_BASE:-https://s4ndmod.com}"
BASE="${BASE%/}"
DEST="${S4ND_DIR:-$HOME/.local/share/s4ndmod26}"
BIN="$HOME/.local/bin"

# The game (not the launcher) needs these libraries to run.  Set S4ND_SKIP_DEPS=1 to skip this step.
if [ -n "${S4ND_SKIP_DEPS:-}" ]; then
  :
elif command -v dnf &>/dev/null; then
  echo "Installing game libraries (dnf)..."
  sudo dnf install -y SDL2 libXScrnSaver libXxf86vm openal-soft libvorbis libcurl 2>/dev/null || true
elif command -v apt-get &>/dev/null; then
  echo "Installing game libraries (apt)..."
  sudo apt-get install -y libsdl2-2.0-0 libxss1 libxxf86vm1 libopenal1 libvorbisfile3 libcurl4 2>/dev/null || true
elif command -v pacman &>/dev/null; then
  echo "Installing game libraries (pacman)..."
  sudo pacman -S --noconfirm sdl2 libxss libxxf86vm openal libvorbis curl 2>/dev/null || true
fi

mkdir -p "$DEST" "$BIN"
echo "Downloading the launcher to $DEST/s4ndmod-launcher"
curl -fsSL "$BASE/downloads/launcher/s4ndmod-launcher-linux" -o "$DEST/s4ndmod-launcher.new"
chmod +x "$DEST/s4ndmod-launcher.new"
mv -f "$DEST/s4ndmod-launcher.new" "$DEST/s4ndmod-launcher"
ln -sf "$DEST/s4ndmod-launcher" "$BIN/s4ndmod-launcher"
echo "Installed. Run it again any time with: s4ndmod-launcher   (make sure $BIN is on your PATH)"

exec "$DEST/s4ndmod-launcher" --server "$BASE"
