#!/bin/bash
# Re-pin locker/power files in the running session (Plasma may rewrite them at start).
set -euo pipefail

cfg="${XDG_CONFIG_HOME:-$HOME/.config}"
mkdir -p "$cfg"
cp -f /etc/xdg/kscreenlockerrc "$cfg/kscreenlockerrc"
cp -f /etc/xdg/powerdevilrc "$cfg/powerdevilrc"
cp -f /etc/xdg/powermanagementprofilesrc "$cfg/powermanagementprofilesrc"
cp -f /etc/xdg/ksmserverrc "$cfg/ksmserverrc"
if command -v kwriteconfig6 >/dev/null 2>&1; then
  kwriteconfig6 --file kcminputrc --group Mouse --key NaturalScroll true
  kwriteconfig6 --file kcminputrc --group Touchpad --key NaturalScroll true
fi
if command -v busctl >/dev/null 2>&1; then
  busctl --user tree org.kde.KWin 2>/dev/null | while read -r line; do
    case "$line" in
      *"/org/kde/KWin/InputDevice/event"*)
        p=${line##* }
        supp=$(busctl --user get-property org.kde.KWin "$p" org.kde.KWin.InputDevice supportsNaturalScroll 2>/dev/null || true)
        case "$supp" in
          *true*)
            busctl --user set-property org.kde.KWin "$p" org.kde.KWin.InputDevice naturalScroll b true || true
            ;;
        esac
        ;;
    esac
  done
fi
if [ -n "${XDG_SESSION_ID:-}" ]; then
  loginctl unlock-session "$XDG_SESSION_ID"
fi
