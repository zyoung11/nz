#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "machine lifecycle"

nzs allow friend >/dev/null 2>&1
nzs allow friend/pc >/dev/null 2>&1
sleep 2

info "block a machine"
out=$(nzs deny friend/pc 2>&1)
expect_contains "machine block done" "$out" "done"
sleep 2
out=$(nzs list 2>&1)
expect_contains "machine shows blocked" "$out" "blocked"
if ping_vpn node-c 192.168.100.1; then
	err "blocked machine still sends traffic"
	FAILS=$((FAILS + 1))
else
	ok "blocked machine cannot send traffic"
fi

info "unblock a machine"
out=$(nzs allow friend/pc 2>&1)
expect_contains "machine unblock done" "$out" "done"
sleep 2
if ping_vpn node-c 192.168.100.1; then
	ok "unblocked machine sends traffic"
else
	err "unblocked machine cannot send traffic"
	FAILS=$((FAILS + 1))
fi

info "block a user covers its machines"
out=$(nzs deny friend 2>&1)
expect_contains "user block done" "$out" "done"
sleep 2
out=$(nzs list 2>&1)
expect_contains "user block shows blocked" "$out" "blocked"
ip_pc=$(machine_ip_by_name pc)
if ping_vpn node-b "$ip_pc"; then
	err "machine of blocked user still sends traffic"
	FAILS=$((FAILS + 1))
else
	ok "machine of blocked user cannot send traffic"
fi

info "unblock a user restores its machines"
out=$(nzs allow friend 2>&1)
expect_contains "user unblock done" "$out" "done"
sleep 2
ip_pc=$(machine_ip_by_name pc)
if ping_vpn node-b "$ip_pc"; then
	ok "machine of unblocked user sends traffic"
else
	err "machine of unblocked user cannot send traffic"
	FAILS=$((FAILS + 1))
fi

info "reject a pending request"
podman exec nz-node-c sh -c 'cp /cfg/state.json /cfg/state.bak'
podman exec nz-node-c rm -f /cfg/state.json
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"tempmachine\",\"domain\":\"nz-server\",\"port\":4399}" > /cfg/config.json'
start_node_daemon node-c
wait_pending tempmachine
out=$(nzs deny friend/tempmachine 2>&1)
expect_contains "pending request rejected" "$out" "done"
sleep 1
out=$(nzs list --pending 2>&1)
expect_not_contains "rejected request gone from pending" "$out" "tempmachine"

info "restoring pc for later suites"
podman exec nz-node-c sh -c 'mv /cfg/state.bak /cfg/state.json'
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"nz-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
sleep 3

finish
