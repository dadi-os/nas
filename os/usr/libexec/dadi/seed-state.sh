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

mkdir -p "$STATE/cloudflared" "$STATE/modules/dwar"

# Ensure cloudflared token file exists (may be empty until set via Nas).
if [ ! -f "$STATE/cloudflared/token" ]; then
  : >"$STATE/cloudflared/token"
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

chmod -R u+rwX,go-rwx "$STATE/modules" "$STATE/cloudflared" 2>/dev/null || true
