#!/usr/bin/env bash
# Public side of the relay, run as root on the Ubuntu VM.
# Usage: sudo ./vm-setup.sh BOX_PUBLIC_KEY
# Forwards TCP 80 and 443 over WireGuard to the box and prints the VM public key.
set -euo pipefail

box_key="${1:-}"
if [ -z "$box_key" ]; then
  echo "usage: vm-setup.sh BOX_PUBLIC_KEY" >&2
  exit 1
fi

apt-get update
apt-get install -y wireguard

umask 077
mkdir -p /etc/wireguard
[ -s /etc/wireguard/vm.key ] || wg genkey >/etc/wireguard/vm.key

cat >/etc/wireguard/dadi-relay.conf <<EOF
[Interface]
Address = 10.77.0.1/30
ListenPort = 51820
PrivateKey = $(cat /etc/wireguard/vm.key)
PostUp = iptables -I INPUT -p udp --dport 51820 -j ACCEPT
PostUp = iptables -I INPUT -p tcp -m multiport --dports 80,443 -j ACCEPT
PostUp = iptables -I FORWARD -d 10.77.0.2 -p tcp -m multiport --dports 80,443 -j ACCEPT
PostUp = iptables -I FORWARD -s 10.77.0.2 -m state --state ESTABLISHED,RELATED -j ACCEPT
PostUp = iptables -t nat -A PREROUTING -p tcp -m multiport --dports 80,443 -j DNAT --to-destination 10.77.0.2
PostUp = iptables -t nat -A POSTROUTING -o dadi-relay -j MASQUERADE
PostDown = iptables -D INPUT -p udp --dport 51820 -j ACCEPT
PostDown = iptables -D INPUT -p tcp -m multiport --dports 80,443 -j ACCEPT
PostDown = iptables -D FORWARD -d 10.77.0.2 -p tcp -m multiport --dports 80,443 -j ACCEPT
PostDown = iptables -D FORWARD -s 10.77.0.2 -m state --state ESTABLISHED,RELATED -j ACCEPT
PostDown = iptables -t nat -D PREROUTING -p tcp -m multiport --dports 80,443 -j DNAT --to-destination 10.77.0.2
PostDown = iptables -t nat -D POSTROUTING -o dadi-relay -j MASQUERADE

[Peer]
PublicKey = $box_key
AllowedIPs = 10.77.0.2/32
EOF

printf 'net.ipv4.ip_forward=1\n' >/etc/sysctl.d/99-dadi-relay.conf
sysctl --system >/dev/null

systemctl enable wg-quick@dadi-relay.service
systemctl restart wg-quick@dadi-relay.service
wg pubkey </etc/wireguard/vm.key
