#!/usr/bin/env bash
# Wait for the stack, provision credentials if needed, run Hath.
set -euo pipefail

cd "$(dirname "$0")/.."

HEALTH_TIMEOUT=120
HATH_LOG_FILE="$(pwd)/.run/hath.log"
export HATH_LOG_FILE

shopt -s nullglob
hath_libs=(../hath/src-tauri/lib/*/libhathnet.a)
shopt -u nullglob
if [ ${#hath_libs[@]} -eq 0 ]; then
  echo "hath-dev: net library missing — running net/build.sh"
  (cd ../hath/net && ./build.sh)
fi

health_ok() {
  local host="$1"
  curl -sf --max-time 2 "http://${host}.dadi/health" >/dev/null
}

wait_for_health() {
  local -a hosts=(dwar yaad dimaag nas)
  local -a pending=("${hosts[@]}")
  local deadline=$((SECONDS + HEALTH_TIMEOUT))

  while [ ${#pending[@]} -gt 0 ]; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "hath-dev: health check timed out after ${HEALTH_TIMEOUT}s" >&2
      local host
      for host in "${pending[@]}"; do
        local service="$host"
        if [ "$host" = "nas" ]; then
          service="nas-service"
        fi
        echo "hath-dev: --- logs: ${service} ---" >&2
        docker compose logs --no-color --tail=80 "$service" >&2 || true
      done
      exit 1
    fi

    local -a still=()
    local host
    for host in "${pending[@]}"; do
      if health_ok "$host"; then
        echo "hath-dev: ${host}.dadi healthy"
      else
        still+=("$host")
      fi
    done
    pending=("${still[@]}")
    if [ ${#pending[@]} -gt 0 ]; then
      sleep 1
    fi
  done
}

wait_for_health

identifier=$(jq -r '.identifier' ../hath/src-tauri/tauri.conf.json)
if [ -z "$identifier" ] || [ "$identifier" = "null" ]; then
  echo "hath-dev: could not read identifier from ../hath/src-tauri/tauri.conf.json" >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin)
    hath_dir="${HOME}/Library/Application Support/${identifier}"
    ;;
  Linux)
    hath_dir="${HOME}/.local/share/${identifier}"
    ;;
  *)
    echo "hath-dev: unsupported platform $(uname -s) for Hath app data" >&2
    exit 1
    ;;
esac

creds_path="${hath_dir}/credentials.json"
if [ ! -f "$creds_path" ]; then
  mkdir -p "$hath_dir"
  echo "hath-dev: provisioning Hath credentials → ${creds_path}"
  bundle=$(curl -sf -X POST http://localhost:8092/provision \
    -H 'content-type: application/json' \
    -d '{"node_name":"hath-dev"}' | jq -r .bundle)
  if [ -z "$bundle" ] || [ "$bundle" = "null" ]; then
    echo "hath-dev: provision returned empty bundle" >&2
    exit 1
  fi
  echo "$bundle" | base64 -d > "$creds_path"
else
  echo "hath-dev: Hath credentials already present — leaving untouched"
fi

mkdir -p .run
: >> "$HATH_LOG_FILE"

cd ../hath && exec npm run tauri dev
