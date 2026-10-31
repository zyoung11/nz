#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "multi user management"

reset_grants

podman exec nz-node-c sh -c 'cp /cfg/state.json /cfg/state.bak'
podman exec nz-node-c rm -f /cfg/state.json
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"bob\",\"password\":\"bobpw\",\"name\":\"bobpc\",\"domain\":\"nz-server\",\"port\":4399}" > /cfg/config.json'
start_node_daemon node-c
wait_pending bob
wait_pending pc

info "admin approves a new user and machine"
out=$(nzs allow bob 2>&1)
expect_contains "user approval done" "$out" "done"
out=$(nzs allow bob/bobpc 2>&1)
expect_contains "machine approval done" "$out" "done"
sleep 6
out=$(nzs list 2>&1)
expect_contains "new user listed" "$out" "bob"

info "users are isolated by default"
ip_pc=$(machine_ip_by_name bobpc)
ip_laptop=$(machine_ip_by_name laptop)
if ping_vpn node-b "$ip_pc"; then
	err "friend machine reached bob machine without grant"
	FAILS=$((FAILS + 1))
else
	ok "friend machine blocked from bob machine"
fi
if ping_vpn node-c "$ip_laptop"; then
	err "bob machine reached friend machine without grant"
	FAILS=$((FAILS + 1))
else
	ok "bob machine blocked from friend machine"
fi

info "admin reaches everyone"
if ping_vpn node-a "$ip_pc"; then
	ok "admin machine reaches bob machine"
else
	err "admin machine cannot reach bob machine"
	FAILS=$((FAILS + 1))
fi

info "grant opens the path to the admin machine"
ip_desktop=$(machine_ip_by_name desktop)
nzs allow friend --to desktop >/dev/null 2>&1
nzs allow bob --to desktop >/dev/null 2>&1
sleep 2
if ping_vpn node-c "$ip_desktop"; then
	ok "granted bob machine reaches the admin machine"
else
	err "granted bob machine cannot reach the admin machine"
	FAILS=$((FAILS + 1))
fi
if ping_vpn node-b "$ip_desktop"; then
	ok "friend machine reaches the admin machine via its own grant"
else
	err "friend machine lost its own grant"
	FAILS=$((FAILS + 1))
fi

info "revoke closes the path again"
nzs deny bob --to desktop >/dev/null 2>&1
sleep 2
if ping_vpn node-c "$ip_desktop"; then
	err "revoked bob machine still reaches the admin machine"
	FAILS=$((FAILS + 1))
else
	ok "revoked path closed"
fi

info "restoring pc for later suites"
nzs deny friend --to desktop >/dev/null 2>&1
nzs deny bob >/dev/null 2>&1
podman exec nz-node-c sh -c 'mv /cfg/state.bak /cfg/state.json'
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"nz-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
sleep 3

finish
