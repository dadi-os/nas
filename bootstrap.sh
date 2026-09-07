#!/bin/sh
set -e

# Wait for Headscale's unix socket / API to answer.
until headscale users list >/dev/null 2>&1; do
  sleep 1
done

# Create the user if absent. Idempotent: this runs on every `up`.
# Headscale 0.26 JSON pretty-prints with spaces — parse with jq, not grep.
if ! headscale users list -o json | jq -e '.[] | select(.name=="ankur")' >/dev/null; then
  headscale users create ankur
fi

# Mint the sidecar's key only if it has not already been written.
# After the sidecar joins, its identity lives in tailscale_state; the key
# file stays so restarts do not mint a second one. `down -v` wipes both.
if [ ! -f /run/bootstrap/authkey ]; then
  # 0.26 --user takes a numeric ID, not a name.
  user_id=$(headscale users list -o json | jq -r '.[] | select(.name=="ankur") | .id')
  if [ -z "$user_id" ] || [ "$user_id" = "null" ]; then
    echo "bootstrap: could not resolve user id for ankur" >&2
    exit 1
  fi
  # Plain-text key on stdout when -o is unset; trim newline for file: readers.
  headscale preauthkeys create --user "$user_id" --reusable --expiration 24h \
    | tr -d '\n' > /run/bootstrap/authkey
fi
