#!/bin/bash
# Create stateful homes on first boot (bootc: /home is machine state).
# dadi is the tty1 console + agent user. SSH admins are created with `dadi users set`.
set -euo pipefail

SKEL=/etc/skel

seed_home() {
  local home="$1"
  local user="$2"
  mkdir -p "$home"
  if [ -d "$SKEL" ]; then
    local src
    while IFS= read -r -d '' src; do
      local rel="${src#"$SKEL"/}"
      local dest="$home/$rel"
      if [ -d "$src" ]; then
        mkdir -p "$dest"
        chown "$user:$user" "$dest"
      elif [ ! -e "$dest" ]; then
        mkdir -p "$(dirname "$dest")"
        cp -a "$src" "$dest"
        chown "$user:$user" "$dest"
      fi
    done < <(find "$SKEL" -mindepth 1 -print0)
  fi
  chown "$user:$user" "$home"
  chmod 0700 "$home"
}

seed_home /var/home/dadi dadi

# The tty1 console autologins dadi with no password. SSH is DenyUsers dadi.
if passwd -S dadi | awk '{exit !($2 == "L")}'; then
  passwd -d dadi
fi
