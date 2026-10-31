#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "relay fallback when the direct path is blocked"

nzs allow friend/pc >/dev/null 2>&1
sleep 2

NODE_C_IP=$(podman inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' nz-node-c)
podman exec nz-node-b iptables -I OUTPUT -d "$NODE_C_IP" -p udp -j DROP
ok "direct path to pc is now a blackhole"

info "waiting for the peer to downgrade to relay"
sleep 12

ip_pc=$(machine_ip_by_name pc)
if ping_vpn node-b "$ip_pc"; then
	ok "relay carries traffic when the direct path is blocked"
else
	err "traffic blackholed when the direct path is blocked"
	FAILS=$((FAILS + 1))
fi

podman exec nz-node-b iptables -D OUTPUT -d "$NODE_C_IP" -p udp -j DROP

finish
