#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "legacy nz and n0 run side by side"

info "building legacy nz"
(cd "$REPO" && CGO_ENABLED=0 go build -o "$BIN_V1" .) || {
	err "legacy build failed"
	finish
}

info "starting legacy nz server in node-a container"
podman cp "$BIN_V1" n0-node-a:/usr/local/bin/nz >/dev/null
printf '%s\n' '{"mode":"server","password":"legacy","port":4242}' | podman exec -i n0-node-a sh -c 'cat > /usr/local/bin/config.json'
podman exec -d n0-node-a sh -c 'cd /usr/local/bin && ./nz run > /cfg/nz.log 2>&1'
sleep 2

if podman exec n0-node-a pgrep -x nz >/dev/null 2>&1; then
	ok "legacy nz running"
else
	err "legacy nz not running: $(podman exec n0-node-a sh -c 'tail -3 /cfg/nz.log 2>/dev/null')"
	FAILS=$((FAILS + 1))
fi

out=$(n0c node-a status 2>&1)
expect_contains "n0 still healthy next to nz" "$out" "running"

out=$(container_log node-a)
expect_contains "n0 tunnel unaffected" "$out" "joined as"

podman exec n0-node-a pkill -x nz >/dev/null 2>&1
ok "legacy nz stopped"

finish
