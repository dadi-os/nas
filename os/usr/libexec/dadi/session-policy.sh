#!/bin/bash
# Re-pin locker/power files in the running session (Plasma may rewrite them at start).
set -euo pipefail

cfg="${XDG_CONFIG_HOME:-$HOME/.config}"
mkdir -p "$cfg"
cp -f /etc/xdg/kscreenlockerrc "$cfg/kscreenlockerrc"
cp -f /etc/xdg/powerdevilrc "$cfg/powerdevilrc"
cp -f /etc/xdg/powermanagementprofilesrc "$cfg/powermanagementprofilesrc"
cp -f /etc/xdg/ksmserverrc "$cfg/ksmserverrc"
loginctl unlock-session
