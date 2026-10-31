#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "full connectivity matrix"

reset_grants

ip_server=192.168.100.1
ip_desktop=$(machine_ip_by_name desktop)
ip_laptop=$(machine_ip_by_name laptop)
ip_pc=$(machine_ip_by_name pc)

expect_path() {
	local label="$1" from="$2" to="$3" want="$4"
	local got="blocked"
	if ping_vpn "$from" "$to"; then
		got="open"
	fi
	expect_eq "$label" "$got" "$want"
}

info "admin machine reaches everyone"
expect_path "desktop to server" node-a "$ip_server" "open"
expect_path "desktop to laptop" node-a "$ip_laptop" "open"
expect_path "desktop to pc" node-a "$ip_pc" "open"

info "same user machines reach each other"
expect_path "laptop to pc" node-b "$ip_pc" "open"
expect_path "pc to laptop" node-c "$ip_laptop" "open"

info "plain user machines never reach admin machines"
expect_path "laptop to server" node-b "$ip_server" "blocked"
expect_path "pc to server" node-c "$ip_server" "blocked"
expect_path "laptop to desktop" node-b "$ip_desktop" "blocked"
expect_path "pc to desktop" node-c "$ip_desktop" "blocked"

info "grant opens exactly one direction"
nzs allow friend/laptop --to desktop >/dev/null 2>&1
sleep 2
expect_path "laptop to desktop after grant" node-b "$ip_desktop" "open"
expect_path "pc to desktop stays blocked" node-c "$ip_desktop" "blocked"
nzs deny friend/laptop --to desktop >/dev/null 2>&1
sleep 2
expect_path "laptop to desktop after revoke" node-b "$ip_desktop" "blocked"

finish
