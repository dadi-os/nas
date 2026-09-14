#!/bin/bash
# Enroll the installed LUKS volume with TPM2 PCR 7 after the first passphrase unlock.
# PCR 7 is Secure Boot policy so bootc kernel upgrades do not invalidate the seal.
# The kickstart passphrase stays in slot 0 as recovery.
set -euo pipefail

STATUS=/var/lib/dadi/tpm.json

write_status() {
  mkdir -p "$(dirname "$STATUS")"
  printf '%s\n' "$1" >"$STATUS"
  chmod 0644 "$STATUS"
}

if [ ! -e /dev/tpmrm0 ] && [ ! -e /dev/tpm0 ]; then
  write_status '{"present":false,"enrolled":false}'
  echo "tpm-enroll: no TPM device" >&2
  exit 1
fi

if [ ! -f /etc/crypttab ]; then
  write_status '{"present":true,"enrolled":false}'
  echo "tpm-enroll: /etc/crypttab missing" >&2
  exit 1
fi

luks_dev=""
crypt_name=""
while read -r name spec _key _opts; do
  case "${name:-}" in
    ''|\#*) continue ;;
  esac
  case "$spec" in
    UUID=*) spec="/dev/disk/by-uuid/${spec#UUID=}" ;;
    PARTUUID=*) spec="/dev/disk/by-partuuid/${spec#PARTUUID=}" ;;
  esac
  if cryptsetup isLuks "$spec" 2>/dev/null; then
    luks_dev=$spec
    crypt_name=$name
    break
  fi
done < /etc/crypttab

if [ -z "$luks_dev" ]; then
  write_status '{"present":true,"enrolled":false}'
  echo "tpm-enroll: no LUKS device in crypttab" >&2
  exit 1
fi

if systemd-cryptenroll "$luks_dev" 2>/dev/null | grep -qi tpm2; then
  write_status "{\"present\":true,\"enrolled\":true,\"pcrs\":\"7\",\"device\":\"${luks_dev}\"}"
  echo "tpm-enroll: already enrolled ${luks_dev}"
  exit 0
fi

systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 "$luks_dev"

tmp=$(mktemp)
awk -v n="$crypt_name" '
  $1 == n && $0 !~ /tpm2-device=/ {
    if (NF >= 4) { print $0 ",tpm2-device=auto"; next }
    print $0 " tpm2-device=auto"
    next
  }
  { print }
' /etc/crypttab >"$tmp"
cat "$tmp" > /etc/crypttab
rm -f "$tmp"

write_status "{\"present\":true,\"enrolled\":true,\"pcrs\":\"7\",\"device\":\"${luks_dev}\"}"
echo "tpm-enroll: enrolled ${luks_dev} pcr7"
