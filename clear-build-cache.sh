#!/bin/bash
# Selectively clear Docker BuildKit cache mounts.
# Usage: ./clear-build-cache.sh            (interactive menu)
#        ./clear-build-cache.sh all        (clear everything)
#
# Note: iortcw client stages use Docker layer cache only (no persistent cache mount),
# so they automatically rebuild clean when iortcw/ files change.

set -e

declare -A CACHES=(
    [game-linux-64]="rtcw-linux-64"
    [game-linux-32]="rtcw-linux-32"
    [game-win-64]="rtcw-win-64"
    [game-win-32]="rtcw-win-32"
    [omnibot]="omnibot-lib-cache"
    [base-paks]="rtcw-main-paks"
)

ORDERED=(game-linux-64 game-linux-32 game-win-64 game-win-32 omnibot base-paks)

prune_cache() {
    local name=$1
    local id=${CACHES[$name]}
    echo "Clearing $name ($id)..."
    docker builder prune --filter type=exec.cachemount --filter "id=$id" --force
}

if [[ "$1" == "all" ]]; then
    for name in "${ORDERED[@]}"; do prune_cache "$name"; done
    echo "All caches cleared."
    exit 0
fi

# Interactive menu
echo "Select caches to clear (space-separated numbers, or 'a' for all):"
echo ""
for i in "${!ORDERED[@]}"; do
    name=${ORDERED[$i]}
    printf "  %d) %-24s  (%s)\n" "$((i+1))" "$name" "${CACHES[$name]}"
done
echo ""
read -rp "Choice: " input

if [[ "$input" == "a" ]]; then
    for name in "${ORDERED[@]}"; do prune_cache "$name"; done
else
    for token in $input; do
        idx=$((token - 1))
        if [[ $idx -ge 0 && $idx -lt ${#ORDERED[@]} ]]; then
            prune_cache "${ORDERED[$idx]}"
        else
            echo "Invalid selection: $token"
        fi
    done
fi

echo "Done."
