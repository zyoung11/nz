#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "node follows server key rotation"

podman exec nz-server rm -f /cfg/state.json
podman exec nz-server pkill -f "nz run" >/dev/null 2>&1
sleep 1
podman exec -d nz-server nz run --config /cfg/config.json --log
ok "server restarted with a fresh identity"

sleep 25

if ping_vpn node-a 192.168.100.1; then
	ok "admin machine recovered after server key rotation"
else
	err "node cannot reach the server after key rotation"
	FAILS=$((FAILS + 1))
fi

finish
