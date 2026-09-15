#!/bin/bash
set -euo pipefail
exec curl -sS -f -m 30 -X POST http://127.0.0.1:8092/headscale/publish
