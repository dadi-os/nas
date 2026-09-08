#!/bin/bash
set -euo pipefail
TOKEN_FILE=/var/lib/dadi/cloudflared/token
if [ ! -s "$TOKEN_FILE" ]; then
  echo "cloudflared: token file empty — set via Nas PUT /cloudflared/token" >&2
  exit 1
fi
exec /usr/local/bin/cloudflared tunnel --no-autoupdate run --token "$(tr -d '\n\r' <"$TOKEN_FILE")"
