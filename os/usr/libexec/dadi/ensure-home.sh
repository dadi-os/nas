#!/bin/bash
# Create stateful home for ankur on first boot (bootc: /home is machine state).
set -euo pipefail

HOME_DIR=/home/ankur
SKEL=/etc/skel

if [ ! -d "$HOME_DIR" ]; then
  mkdir -p "$HOME_DIR"
  if [ -d "$SKEL" ]; then
    cp -a "$SKEL"/. "$HOME_DIR"/
  fi
  chown -R ankur:ankur "$HOME_DIR"
  chmod 0700 "$HOME_DIR"
fi

# Always ensure ownership in case of partial first boot.
chown ankur:ankur "$HOME_DIR"
