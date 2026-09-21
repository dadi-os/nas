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

mkdir -p "$STATE/caddy" "$STATE/modules/dwar" "$STATE/modules/chaavi" "$STATE/headscale"

if [ ! -f "$STATE/headscale/config.yaml" ]; then
  cp /etc/headscale/config.yaml "$STATE/headscale/config.yaml"
  chmod 0600 "$STATE/headscale/config.yaml"
fi

if [ ! -f "$STATE/caddy/headscale.caddy" ]; then
  printf '%s\n' "# unpublished" >"$STATE/caddy/headscale.caddy"
  chmod 0644 "$STATE/caddy/headscale.caddy"
fi

# Mesh CA + chaavi.dadi leaf for Caddy HTTPS (Bitwarden). Nas also ensures
# these on startup via Go; seed covers the case where Caddy starts first.
TLS="$STATE/caddy/tls"
mkdir -p "$TLS"
if [ ! -f "$TLS/ca.crt" ] || [ ! -f "$TLS/chaavi.crt" ] || [ ! -f "$TLS/ca.key" ] || [ ! -f "$TLS/chaavi.key" ]; then
  if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl is required to seed mesh TLS under $TLS" >&2
    exit 1
  fi
  openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -keyout "$TLS/ca.key" -out "$TLS/ca.crt" \
    -subj "/CN=dadi mesh CA"
  openssl req -newkey rsa:2048 -sha256 -nodes \
    -keyout "$TLS/chaavi.key" -out "$TLS/chaavi.csr" \
    -subj "/CN=chaavi.dadi"
  openssl x509 -req -in "$TLS/chaavi.csr" -CA "$TLS/ca.crt" -CAkey "$TLS/ca.key" \
    -CAcreateserial -out "$TLS/chaavi.crt" -days 825 -sha256 \
    -extfile <(printf 'subjectAltName=DNS:chaavi.dadi')
  rm -f "$TLS/chaavi.csr" "$TLS/ca.srl"
  chmod 0600 "$TLS/ca.key" "$TLS/chaavi.key"
  chmod 0644 "$TLS/ca.crt" "$TLS/chaavi.crt"
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

if [ ! -f "$STATE/modules/chaavi/.env" ]; then
  cat >"$STATE/modules/chaavi/.env" <<'EOF'
BW_CLIENTID=
BW_CLIENTSECRET=
BW_PASSWORD=
EOF
fi

chmod -R u+rwX,go-rwx "$STATE/modules" "$STATE/caddy" "$STATE/headscale" 2>/dev/null || true
