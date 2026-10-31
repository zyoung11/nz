#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "trust opens friend to admin"

ip_desktop=$(machine_ip_by_name desktop)

if ping_vpn node-b "$ip_desktop"; then
	err "friend reached admin without trust"
	FAILS=$((FAILS + 1))
else
	ok "friend blocked from admin before trust"
fi

n0s allow friend/laptop --to desktop
sleep 1

if ping_vpn node-b "$ip_desktop"; then
	ok "trusted machine reaches admin machine"
else
	err "trusted machine cannot reach admin machine"
	FAILS=$((FAILS + 1))
fi

n0s deny friend/laptop --to desktop
sleep 1

if ping_vpn node-b "$ip_desktop"; then
	err "revoked machine still reaches admin"
	FAILS=$((FAILS + 1))
else
	ok "revoked machine blocked again"
fi

finish
