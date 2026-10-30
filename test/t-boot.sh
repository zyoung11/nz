#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "boot resilience"

n0s trust friend/laptop >/dev/null 2>&1
ip_laptop=$(machine_ip_by_name laptop)

info "daemon survives a hard kill"
podman exec n0-node-b pkill -9 -f "n0 run" >/dev/null 2>&1
sleep 1
start_node_daemon node-b
wait_joined node-b 15
expect_eq "ip reused after hard kill" "$(machine_ip_by_name laptop)" "$ip_laptop"

info "stale socket file is cleaned on start"
podman exec n0-node-b sh -c 'rm -f /tmp/n0.sock && touch /tmp/n0.sock'
start_node_daemon node-b
out=$(timeout 8 podman exec n0-node-b n0 status 2>&1)
expect_contains "daemon answers despite stale socket" "$out" "running"

info "stale tun device is recreated on start"
podman exec n0-node-b pkill -9 -f "n0 run" >/dev/null 2>&1
sleep 1
start_node_daemon node-b
wait_joined node-b 15
if ping_vpn node-b 192.168.200.1; then
	ok "traffic flows after tun recreation"
else
	err "traffic broken after tun recreation"
	FAILS=$((FAILS + 1))
fi

info "corrupt state file reports an error"
podman exec n0-node-b sh -c 'cp /cfg/state.json /cfg/state.bak && echo broken > /cfg/state.json'
start_node_daemon node-b
sleep 3
out=$(container_log node-b)
expect_contains "corrupt state rejected" "$out" "failed to parse state"
podman exec n0-node-b sh -c 'mv /cfg/state.bak /cfg/state.json'
start_node_daemon node-b
wait_joined node-b 15

info "daemon waits when the server name does not resolve"
podman exec n0-node-b sh -c 'cp /cfg/config.json /cfg/config.bak'
podman exec n0-node-b sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"laptop\",\"domain\":\"not-yet.invalid\",\"port\":4399}" > /cfg/config.json'
podman exec n0-node-b n0 reload >/dev/null 2>&1
sleep 4
out=$(timeout 8 podman exec n0-node-b n0 status 2>&1)
expect_contains "daemon alive while the server is unreachable" "$out" "running"
podman exec n0-node-b sh -c 'mv /cfg/config.bak /cfg/config.json'
podman exec n0-node-b n0 reload >/dev/null 2>&1
sleep 4
out=$(timeout 8 podman exec n0-node-b n0 status 2>&1)
expect_contains "connection recovered after the server appears" "$out" "joined"

n0s untrust friend/laptop >/dev/null 2>&1

finish
