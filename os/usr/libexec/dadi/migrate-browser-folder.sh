#!/bin/bash
# One-time move of a pre-helper browser slot folder to an agent's first Chromium folder:
# $STATE/browsers/<from-slot> becomes $STATE/browsers/<agent>/1, keeping its sign-ins and
# remembered devices (the networking specialist's LinkedIn device trust lives in slot 10).
# Run with nas.service stopped. Usage: migrate-browser-folder.sh <state-dir> <from-slot> <agent-id>
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <state-dir> <from-slot> <agent-id>" >&2
  exit 2
fi
state="$1"
slot="$2"
agent="$3"

case "$slot" in
  '' | *[!0-9]*) echo "from-slot must be a number: $slot" >&2; exit 2 ;;
esac
case "$agent" in
  '' | . | .. | */* | *\*) echo "agent-id cannot name a folder: $agent" >&2; exit 2 ;;
esac

from="$state/browsers/$slot"
to="$state/browsers/$agent/1"

[ -d "$from" ] || { echo "no folder at $from" >&2; exit 1; }
[ ! -e "$to" ] || { echo "$to already exists" >&2; exit 1; }
if systemctl is-active --quiet nas.service; then
  echo "nas.service is running; stop it first (systemctl stop nas)" >&2
  exit 1
fi
if pgrep -f "user-data-dir=$from" > /dev/null; then
  echo "a Chromium is still running on $from" >&2
  exit 1
fi

owner="$(stat -c '%u:%g' "$from")"
mkdir -p "$state/browsers/$agent"
chown "$owner" "$state/browsers/$agent"
mv "$from" "$to"
rm -f "$to/SingletonLock" "$to/SingletonCookie" "$to/SingletonSocket"
echo "moved $from to $to"
