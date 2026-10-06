#!/bin/bash
# Apply dadi look-and-feel and place crest widgets. Re-runs when LAYOUT_VERSION changes or when
# the live desktop containment lacks the layout's Folder View url, which happens when Plasma
# replaces the desktop containment with a default one.
set -euo pipefail

LAYOUT_VERSION=7
DESKTOP_URL=file:///usr/share/dadi/desktop
FLAG="${XDG_CONFIG_HOME:-$HOME/.config}/dadi/lnf-version"
LAYOUT=/usr/share/plasma/look-and-feel/org.dadi.desktop/contents/layouts/org.kde.plasma.desktop-layout.js
PLACE=/usr/share/dadi/plasma/place-widgets.js
FORCE=0
if [ "${1:-}" = "--force" ]; then
  FORCE=1
fi

# wait_plasmashell waits until plasmashell answers scripts and has a desktop for the current activity.
wait_plasmashell() {
  local i
  for i in $(seq 1 120); do
    if busctl --user call org.kde.plasmashell /PlasmaShell org.kde.PlasmaShell evaluateScript s \
      'if (desktopsForActivity(currentActivity()).length < 1) throw "no desktop"; print("ok")' >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "plasmashell D-Bus not ready" >&2
  return 1
}

# live_desktop_url prints the Folder View url of the desktop the layout configures, as busctl renders it.
live_desktop_url() {
  busctl --user call org.kde.plasmashell /PlasmaShell org.kde.PlasmaShell evaluateScript s \
    'var d = desktops()[0]; d.currentConfigGroup = ["General"]; print(d.readConfig("url", ""))'
}

wait_plasmashell
if [ "$FORCE" -eq 0 ] && [ -f "$FLAG" ] && [ "$(cat "$FLAG")" = "$LAYOUT_VERSION" ] &&
  [ "$(live_desktop_url)" = "s \"$DESKTOP_URL\"" ]; then
  exit 0
fi
busctl --user call org.kde.plasmashell /PlasmaShell org.kde.PlasmaShell evaluateScript s "$(cat "$LAYOUT" "$PLACE")" >/dev/null
applied="$(live_desktop_url)"
if [ "$applied" != "s \"$DESKTOP_URL\"" ]; then
  echo "desktop url not applied: $applied" >&2
  exit 1
fi
mkdir -p "$(dirname "$FLAG")"
printf '%s\n' "$LAYOUT_VERSION" > "$FLAG"
