#!/bin/bash
# Enroll the installed LUKS volume with TPM2 PCR 7.
# After initrd unlock the volume key is a kernel logon key and cannot be read back,
# so systemd-cryptenroll needs the kickstart passphrase from luks-enroll.key
# (written by the installer %post, shredded after a successful first-boot seal).
# PCR 7 is Secure Boot policy so bootc kernel upgrades do not invalidate the seal.
# Slot 0 (the kickstart passphrase) stays as recovery.
set -euo pipefail

STATUS=/var/lib/dadi/tpm.json
ENROLL_KEY=/var/lib/dadi/luks-enroll.key

keep_key=0
if [ "${1:-}" = "--keep-key" ]; then
  keep_key=1
fi

write_status() {
  mkdir -p "$(dirname "$STATUS")"
  printf '%s\n' "$1" >"$STATUS"
  chmod 0644 "$STATUS"
}

ensure_crypttab_tpm2() {
  local name=$1
  tmp=$(mktemp)
  awk -v n="$name" '
    $1 == n && $0 !~ /tpm2-device=/ {
      if (NF >= 4) { print $0 ",tpm2-device=auto"; next }
      print $0 " tpm2-device=auto"
      next
    }
    { print }
  ' /etc/crypttab >"$tmp"
  cat "$tmp" > /etc/crypttab
  rm -f "$tmp"
}

has_tpm2_slot() {
  systemd-cryptenroll "$1" 2>/dev/null | grep -qi tpm2
}

shred_enroll_key() {
  if [ "$keep_key" = 1 ]; then
    return 0
  fi
  if [ -f "$ENROLL_KEY" ]; then
    shred -u "$ENROLL_KEY"
  fi
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

if [ -f "$ENROLL_KEY" ]; then
  if has_tpm2_slot "$luks_dev"; then
    systemd-cryptenroll --wipe-slot=tpm2 "$luks_dev"
  fi
  if ! timeout 60 systemd-cryptenroll --unlock-key-file="$ENROLL_KEY" --tpm2-device=auto --tpm2-pcrs=7 --tpm2-with-pin=no "$luks_dev"; then
    write_status "{\"present\":true,\"enrolled\":false,\"device\":\"${luks_dev}\"}"
    echo "tpm-enroll: systemd-cryptenroll failed for ${luks_dev}" >&2
    exit 1
  fi
  ensure_crypttab_tpm2 "$crypt_name"
  write_status "{\"present\":true,\"enrolled\":true,\"pcrs\":\"7\",\"device\":\"${luks_dev}\"}"
  shred_enroll_key
  echo "tpm-enroll: enrolled ${luks_dev} pcr7"
  exit 0
fi

if has_tpm2_slot "$luks_dev"; then
  ensure_crypttab_tpm2 "$crypt_name"
  write_status "{\"present\":true,\"enrolled\":true,\"pcrs\":\"7\",\"device\":\"${luks_dev}\"}"
  echo "tpm-enroll: already enrolled ${luks_dev}"
  exit 0
fi

write_status "{\"present\":true,\"enrolled\":false,\"device\":\"${luks_dev}\"}"
echo "tpm-enroll: missing ${ENROLL_KEY} (kernel logon keys cannot unlock cryptenroll; the installer must drop the kickstart passphrase there)" >&2
exit 1
