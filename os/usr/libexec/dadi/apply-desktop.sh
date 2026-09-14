#!/bin/bash
# Apply dadi look-and-feel and place crest widgets. Re-runs when LAYOUT_VERSION changes.
set -euo pipefail

LAYOUT_VERSION=2
FLAG="${XDG_CONFIG_HOME:-$HOME/.config}/dadi/lnf-version"
FORCE=0
if [ "${1:-}" = "--force" ]; then
  FORCE=1
fi

if [ "$FORCE" -eq 0 ] && [ -f "$FLAG" ] && [ "$(cat "$FLAG")" = "$LAYOUT_VERSION" ]; then
  exit 0
fi

wait_plasmashell() {
  local i
  for i in $(seq 1 40); do
    if qdbus6 org.kde.plasmashell /PlasmaShell org.kde.PlasmaShell.evaluateScript 'print("ok")' >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "plasmashell D-Bus not ready" >&2
  return 1
}

wait_plasmashell
lookandfeeltool -a org.dadi.desktop --resetLayout
qdbus6 org.kde.plasmashell /PlasmaShell org.kde.PlasmaShell.evaluateScript \
  "$(cat /usr/share/dadi/plasma/place-widgets.js)"
mkdir -p "$(dirname "$FLAG")"
printf '%s\n' "$LAYOUT_VERSION" > "$FLAG"
