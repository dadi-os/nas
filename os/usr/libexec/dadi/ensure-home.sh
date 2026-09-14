#!/bin/bash
# Create stateful homes on first boot (bootc: /home is machine state).
# dadi is the graphical session + agent user. SSH admins are created in Preferences.
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

seed_home /var/lib/dadi dadi

# Locked shadow (passwd -l) fails PAM account in SDDM autologin. SSH is DenyUsers dadi.
if passwd -S dadi | awk '{exit !($2 == "L")}'; then
  passwd -d dadi
fi

# Plasma writes ~/.config on first run and can restore Autolock. Re-pin every boot.
install -d -o dadi -g dadi -m 0700 /var/lib/dadi/.config
install -o dadi -g dadi -m 0600 /etc/xdg/kscreenlockerrc /var/lib/dadi/.config/kscreenlockerrc
install -o dadi -g dadi -m 0600 /etc/xdg/powerdevilrc /var/lib/dadi/.config/powerdevilrc
install -o dadi -g dadi -m 0600 /etc/xdg/powermanagementprofilesrc /var/lib/dadi/.config/powermanagementprofilesrc
install -o dadi -g dadi -m 0600 /etc/xdg/ksmserverrc /var/lib/dadi/.config/ksmserverrc
