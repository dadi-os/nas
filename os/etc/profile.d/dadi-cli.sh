# Prefer a live overlay binary when present.
if [ -x /var/lib/dadi/bin/dadi ]; then
  case ":${PATH}:" in
    *:/var/lib/dadi/bin:*) ;;
    *) PATH="/var/lib/dadi/bin:${PATH}" ;;
  esac
elif [ -x "${HOME}/.local/bin/dadi" ] && [ -x /usr/bin/dadi ]; then
  PATH="/usr/bin:${PATH}"
fi
# Host CLI talks to Dimaag on the appliance loopback publish port.
export DIMAAG_URL=http://127.0.0.1:8083
if [ -n "${BASH_VERSION-}" ] && [ -n "${PS1-}" ]; then
  _dadi_bin="$(command -v dadi 2>/dev/null)" || true
  if [ -n "${_dadi_bin}" ]; then
    complete -o default -C "${_dadi_bin}" dadi
  fi
  unset _dadi_bin
fi
