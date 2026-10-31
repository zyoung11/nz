#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "edge cases"

n0s allow friend >/dev/null 2>&1
n0s allow friend/pc >/dev/null 2>&1
sleep 2

info "repeated approval is idempotent"
out=$(n0s allow friend/laptop 2>&1)
expect_contains "approve an active machine is a grant" "$out" "done"

info "repeated block is idempotent"
n0s deny friend/laptop >/dev/null 2>&1
out=$(n0s deny friend/laptop 2>&1)
expect_contains "block a blocked machine is fine" "$out" "done"
n0s allow friend/laptop >/dev/null 2>&1
sleep 1

info "bad target syntax reports clearly"
out=$(n0s allow 2>&1)
expect_contains "missing target reported" "$out" "needs one target"
out=$(n0s allow a b 2>&1)
expect_contains "two targets reported" "$out" "needs one target"
out=$(n0s allow /x 2>&1)
expect_contains "empty user reported" "$out" "no user part"

info "pending machine carries no traffic"
podman exec n0-node-c sh -c 'cp /cfg/state.json /cfg/state.bak'
podman exec n0-node-c rm -f /cfg/state.json
podman exec n0-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"ghost\",\"domain\":\"n0-server\",\"port\":4399}" > /cfg/config.json'
start_node_daemon node-c
wait_pending ghost
ip_ghost=$(machine_ip_by_name ghost)
if [ -z "$ip_ghost" ]; then
	ok "pending machine has no tunnel yet"
else
	err "pending machine has a tunnel already"
	FAILS=$((FAILS + 1))
fi
podman exec n0-node-c sh -c 'mv /cfg/state.bak /cfg/state.json'
podman exec n0-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"n0-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
sleep 3

info "duplicate registration keeps one record"
start_node_daemon node-b
sleep 3
out=$(n0s list 2>&1)
count=$(echo "$out" | grep -c "laptop")
if [ "$count" -eq 1 ]; then
	ok "one record per machine"
else
	err "duplicate machine records: $count"
	FAILS=$((FAILS + 1))
fi

finish
