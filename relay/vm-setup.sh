#!/usr/bin/env bash
# Public side of the relay, run as root on the Oracle Linux 9 VM.
# Usage: sudo ./vm-setup.sh BOX_PUBLIC_KEY
# Forwards TCP 80 and 443 over WireGuard to the box and prints the VM public key.
set -euo pipefail

box_key="${1:-}"
if [ -z "$box_key" ]; then
  echo "usage: vm-setup.sh BOX_PUBLIC_KEY" >&2
  exit 1
fi

dnf install -y oracle-epel-release-el9
dnf install -y wireguard-tools
modprobe wireguard

umask 077
mkdir -p /etc/wireguard
[ -s /etc/wireguard/vm.key ] || wg genkey >/etc/wireguard/vm.key

cat >/etc/wireguard/dadi-relay.conf <<EOF
[Interface]
Address = 10.77.0.1/30
ListenPort = 51820
PrivateKey = $(cat /etc/wireguard/vm.key)

[Peer]
PublicKey = $box_key
AllowedIPs = 10.77.0.2/32
EOF

printf 'net.ipv4.ip_forward=1\n' >/etc/sysctl.d/99-dadi-relay.conf
sysctl --system >/dev/null

firewall_ensure() {
  firewall-cmd --permanent --query-"$1" >/dev/null 2>&1 || firewall-cmd --permanent --add-"$1"
}
firewall_ensure port=51820/udp
firewall_ensure masquerade
firewall_ensure forward
firewall_ensure forward-port=port=80:proto=tcp:toaddr=10.77.0.2
firewall_ensure forward-port=port=443:proto=tcp:toaddr=10.77.0.2
firewall-cmd --reload

systemctl enable wg-quick@dadi-relay.service
systemctl restart wg-quick@dadi-relay.service
wg pubkey </etc/wireguard/vm.key
