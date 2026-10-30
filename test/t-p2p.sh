#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "same user machines reach each other"

n0s allow friend/pc
sleep 2
wait_joined node-c 10

ip_pc=$(machine_ip_by_name pc)
ip_laptop=$(machine_ip_by_name laptop)
info "pc=$ip_pc laptop=$ip_laptop"

if ping_vpn node-b "$ip_pc"; then
	ok "laptop reaches pc"
else
	err "laptop cannot reach pc"
	FAILS=$((FAILS + 1))
fi

if ping_vpn node-c "$ip_laptop"; then
	ok "pc reaches laptop"
else
	err "pc cannot reach laptop"
	FAILS=$((FAILS + 1))
fi

finish
