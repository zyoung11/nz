#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "block and unblock"

ip_laptop=$(machine_ip_by_name laptop)
ip_pc=$(machine_ip_by_name pc)

nzs deny friend/laptop
sleep 3

if ping_vpn node-c "$ip_laptop"; then
	err "blocked machine still reachable"
	FAILS=$((FAILS + 1))
else
	ok "blocked machine unreachable"
fi

if ping_vpn node-b "$ip_pc"; then
	err "blocked machine still sends traffic"
	FAILS=$((FAILS + 1))
else
	ok "blocked machine cannot send traffic"
fi

nzs allow friend/laptop
sleep 3
nzs allow friend/laptop >/dev/null 2>&1
start_node_daemon node-b
wait_joined node-b 10

if ping_vpn node-c "$ip_laptop"; then
	ok "unblocked machine reachable again"
else
	err "unblocked machine still unreachable"
	FAILS=$((FAILS + 1))
fi

finish
