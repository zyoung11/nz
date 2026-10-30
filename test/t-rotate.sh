#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "node follows server key rotation"

podman exec n0-server rm -f /cfg/n0.key
podman exec n0-server pkill -f "n0 run" >/dev/null 2>&1
sleep 1
podman exec -d n0-server n0 run --config /cfg/config.json
ok "server restarted with a fresh key"

sleep 25

if ping_vpn node-a 192.168.200.1; then
	ok "admin machine recovered after server key rotation"
else
	err "node cannot reach the server after key rotation"
	FAILS=$((FAILS + 1))
fi

finish
