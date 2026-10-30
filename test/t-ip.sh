#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "ip allocation and reuse"

ip1=$(machine_ip_by_name laptop)
if [ -z "$ip1" ]; then
	err "no ip for laptop"
	finish
fi
info "laptop ip is $ip1"

start_node_daemon node-b
wait_joined node-b 10

ip2=$(machine_ip_by_name laptop)
expect_eq "ip reused after reconnect" "$ip2" "$ip1"

finish
