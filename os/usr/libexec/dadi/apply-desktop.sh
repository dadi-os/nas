#!/bin/bash
# Apply dadi look-and-feel + Bloom field once per user (first Plasma session).
set -euo pipefail

FLAG="${XDG_CONFIG_HOME:-$HOME/.config}/dadi/lnf-applied"
if [ -f "$FLAG" ]; then
  exit 0
fi

if command -v lookandfeeltool >/dev/null 2>&1; then
  lookandfeeltool -a org.dadi.desktop --resetLayout
fi

if command -v plasma-apply-wallpaperimage >/dev/null 2>&1; then
  plasma-apply-wallpaperimage \
    /usr/share/wallpapers/DadiBloom/contents/images/1920x1080.png || true
fi

mkdir -p "$(dirname "$FLAG")"
touch "$FLAG"
