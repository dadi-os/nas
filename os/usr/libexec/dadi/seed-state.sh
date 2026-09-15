#!/bin/bash
# Seed /var/lib/dadi from /usr/share/dadi/seed when missing (first boot / fresh volume).
set -euo pipefail

STATE="${DADI_STATE_DIR:-/var/lib/dadi}"
SEED=/usr/share/dadi/seed

mkdir -p "$STATE"

copy_if_missing() {
  local rel="$1"
  local dest="$STATE/$rel"
  local src="$SEED/$rel"
  if [ ! -e "$dest" ]; then
    mkdir -p "$(dirname "$dest")"
    if [ -d "$src" ]; then
      cp -a "$src" "$dest"
    elif [ -f "$src" ]; then
      cp -a "$src" "$dest"
    else
      # create empty file/dir placeholders from seed tree walk below
      :
    fi
  fi
}

# Walk seed tree and copy any path that does not yet exist under STATE.
while IFS= read -r -d '' src; do
  rel="${src#"$SEED"/}"
  dest="$STATE/$rel"
  if [ -d "$src" ]; then
    mkdir -p "$dest"
  elif [ ! -e "$dest" ]; then
    mkdir -p "$(dirname "$dest")"
    cp -a "$src" "$dest"
  fi
done < <(find "$SEED" -print0)

mkdir -p "$STATE/caddy" "$STATE/modules/dwar" "$STATE/headscale"

# Control URL preference file (Preferences → Tunnel). Empty until set or seeded.
if [ ! -f "$STATE/headscale/control_url" ]; then
  : >"$STATE/headscale/control_url"
  chmod 0600 "$STATE/headscale/control_url"
fi
if [ ! -s "$STATE/headscale/control_url" ] && [ -n "${CONTROL_URL:-}" ]; then
  printf '%s\n' "$CONTROL_URL" >"$STATE/headscale/control_url"
  chmod 0600 "$STATE/headscale/control_url"
fi

if [ ! -f "$STATE/headscale/config.yaml" ]; then
  cp /etc/headscale/config.yaml "$STATE/headscale/config.yaml"
  chmod 0600 "$STATE/headscale/config.yaml"
fi

if [ ! -f "$STATE/caddy/headscale.caddy" ]; then
  printf '%s\n' "# unpublished" >"$STATE/caddy/headscale.caddy"
  chmod 0644 "$STATE/caddy/headscale.caddy"
fi

# Ensure dwar .env exists (blank keys until set via Preferences).
if [ ! -f "$STATE/modules/dwar/.env" ]; then
  cat >"$STATE/modules/dwar/.env" <<'EOF'
ANTHROPIC_API_KEY=
GEMINI_API_KEY=
OPENAI_API_KEY=
DEEPGRAM_API_KEY=
EOF
fi

chmod -R u+rwX,go-rwx "$STATE/modules" "$STATE/caddy" "$STATE/headscale" 2>/dev/null || true
