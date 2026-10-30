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

info "pinned ip from node config"
n0s deny friend/pc >/dev/null 2>&1
podman exec n0-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"n0-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
wait_pending pc
n0s allow friend >/dev/null 2>&1
n0s allow friend/pc >/dev/null 2>&1
sleep 1
expect_eq "pinned ip granted" "$(machine_ip_by_name pc)" "192.168.200.7"

info "conflicting pin refused"
podman exec n0-node-c rm -f /cfg/state.json
podman exec n0-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc2\",\"domain\":\"n0-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
wait_pending pc2
out=$(n0s allow friend/pc2 2>&1)
expect_contains "conflicting pin refused" "$out" "already used"

info "restoring pc for later suites"
n0s deny friend/pc2 >/dev/null 2>&1
n0s deny friend/pc >/dev/null 2>&1
podman exec n0-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"n0-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
wait_pending pc

finish
