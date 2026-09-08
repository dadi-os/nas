#!/bin/bash
# Ensure Hath AppImage exists under /var/lib/dadi/hath (download latest release if missing).
set -euo pipefail

DIR=/var/lib/dadi/hath
APP="$DIR/hath.AppImage"
mkdir -p "$DIR"

if [ -x "$APP" ]; then
  exit 0
fi

# Prefer a pre-placed asset; otherwise pull from GitHub Releases (public).
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

API="https://api.github.com/repos/dadi-os/hath/releases/latest"
URL="$(curl -fsSL "$API" | jq -r '.assets[] | select(.name=="hath-linux-x86_64.AppImage") | .browser_download_url')"
if [ -z "$URL" ] || [ "$URL" = "null" ]; then
  echo "ensure-hath: no hath-linux-x86_64.AppImage on latest release" >&2
  exit 1
fi

curl -fsSL -o "$TMP" "$URL"
chmod +x "$TMP"
mv "$TMP" "$APP"
trap - EXIT
