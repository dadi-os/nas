#!/bin/sh
set -e

authkey=/var/lib/dadi/bootstrap/authkey
until [ -s "$authkey" ]; do
  sleep 1
done

key=$(cat "$authkey")

# Host is the mesh node (MagicDNS: os.dadi). Traffic to *.dadi extra
# records lands on this node; Caddy on :80 routes by Host header.
exec tailscale up \
  --login-server=http://127.0.0.1:8080 \
  --authkey="$key" \
  --hostname=os \
  --accept-dns=false \
  --reset
