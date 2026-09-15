#!/bin/bash
set -euo pipefail
URL_FILE=/var/lib/dadi/headscale/control_url
if [ ! -s "$URL_FILE" ]; then
  exit 0
fi
if [ -z "$(tr -d '[:space:]' <"$URL_FILE")" ]; then
  exit 0
fi
exec curl -sS -f -m 30 -X POST http://127.0.0.1:8092/headscale/publish
