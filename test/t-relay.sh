#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "relay direction on one way pair"

ip_desktop=$(machine_ip_by_name desktop)
ip_laptop=$(machine_ip_by_name laptop)
info "desktop=$ip_desktop laptop=$ip_laptop"

if ping_vpn node-a "$ip_laptop"; then
	ok "admin machine reaches friend machine"
else
	err "admin machine cannot reach friend machine"
	FAILS=$((FAILS + 1))
fi

if ping_vpn node-b "$ip_desktop"; then
	err "friend machine reached admin machine without trust"
	FAILS=$((FAILS + 1))
else
	ok "friend machine blocked from admin machine"
fi

finish
