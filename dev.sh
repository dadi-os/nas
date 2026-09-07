#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

HOSTS_LINE="127.0.0.1  dwar.dadi yaad.dadi dimaag.dadi nas.dadi"
HEALTH_TIMEOUT=60

# --- Step 1: Preflight -------------------------------------------------------

ensure_env() {
  local module="$1"
  local env_path="../${module}/.env"
  local example_path="../${module}/.env.example"
  if [ ! -f "$env_path" ]; then
    if [ ! -f "$example_path" ]; then
      echo "dev: missing ${env_path} and no .env.example to copy from" >&2
      exit 1
    fi
    cp "$example_path" "$env_path"
    echo "dev: created ${env_path} from .env.example"
  fi
}

ensure_env dwar
ensure_env yaad
ensure_env dimaag

if ! grep -q '\.dadi' /etc/hosts; then
  echo "dev: /etc/hosts is missing .dadi entries. Add this line and re-run:" >&2
  echo "  ${HOSTS_LINE}" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "dev: docker is not running" >&2
  exit 1
fi

if [ ! -d ../hath ]; then
  echo "dev: ../hath does not exist" >&2
  exit 1
fi

shopt -s nullglob
hath_libs=(../hath/src-tauri/lib/*/libhathnet.a)
shopt -u nullglob
if [ ${#hath_libs[@]} -eq 0 ]; then
  echo "dev: hath net library missing — running net/build.sh"
  (cd ../hath/net && ./build.sh)
fi

# --- Step 2: Services ---------------------------------------------------------

docker compose up -d --build

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
      echo "dev: health check timed out after ${HEALTH_TIMEOUT}s" >&2
      local host
      for host in "${pending[@]}"; do
        local service="$host"
        if [ "$host" = "nas" ]; then
          service="nas-service"
        fi
        echo "dev: --- logs: ${service} ---" >&2
        docker compose logs --no-color --tail=80 "$service" >&2 || true
      done
      exit 1
    fi

    local -a still=()
    local host
    for host in "${pending[@]}"; do
      if health_ok "$host"; then
        echo "dev: ${host}.dadi healthy"
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

# --- Step 3: Migrations -------------------------------------------------------

docker compose run --rm yaad npm run db:migrate
docker compose run --rm dimaag npm run db:migrate

# --- Step 4: Hath credentials -------------------------------------------------

identifier=$(jq -r '.identifier' ../hath/src-tauri/tauri.conf.json)
if [ -z "$identifier" ] || [ "$identifier" = "null" ]; then
  echo "dev: could not read identifier from ../hath/src-tauri/tauri.conf.json" >&2
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
    echo "dev: unsupported platform $(uname -s) for Hath app data" >&2
    exit 1
    ;;
esac

creds_path="${hath_dir}/credentials.json"
if [ ! -f "$creds_path" ]; then
  mkdir -p "$hath_dir"
  echo "dev: provisioning Hath credentials → ${creds_path}"
  bundle=$(curl -sf -X POST http://localhost:8092/provision \
    -H 'content-type: application/json' \
    -d '{"node_name":"hath-dev"}' | jq -r .bundle)
  if [ -z "$bundle" ] || [ "$bundle" = "null" ]; then
    echo "dev: provision returned empty bundle" >&2
    exit 1
  fi
  echo "$bundle" | base64 -d > "$creds_path"
else
  echo "dev: Hath credentials already present — leaving untouched"
fi

# --- Step 5: Hath -------------------------------------------------------------

cd ../hath && npm run tauri dev
