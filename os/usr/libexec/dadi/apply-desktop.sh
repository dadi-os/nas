#!/bin/bash
# Apply dadi look-and-feel + layout once per user (first Plasma session).
set -euo pipefail

FLAG="${XDG_CONFIG_HOME:-$HOME/.config}/dadi/lnf-applied"
if [ -f "$FLAG" ]; then
  exit 0
fi

if command -v lookandfeeltool >/dev/null 2>&1; then
  lookandfeeltool -a org.dadi.desktop --resetLayout
fi

mkdir -p "$(dirname "$FLAG")"
touch "$FLAG"
