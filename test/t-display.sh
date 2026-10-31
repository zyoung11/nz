#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "cli display semantics"

nzs allow friend >/dev/null 2>&1
nzs allow friend/pc >/dev/null 2>&1
sleep 2

info "local row follows the querying machine"
out=$(podman exec nz-server nz list 2>&1)
echo "$out" | grep -q "server" && expect_contains "server view marks server local" "$out" "local"
out=$(podman exec nz-node-a nz list 2>&1)
expect_contains "node view marks itself local" "$out" "local"
expect_contains "node view shows its own user group" "$out" "me"

info "status words are consistent"
out=$(podman exec nz-server nz list --pending 2>&1)
expect_contains "pending view lists pending" "$out" "pending"

info "aliases match the real command"
out1=$(podman exec nz-server nz version 2>&1)
out2=$(podman exec nz-server nz -v 2>&1)
out3=$(podman exec nz-server nz --version 2>&1)
if [ "$out1" = "$out2" ] && [ "$out1" = "$out3" ]; then
	ok "version aliases match"
else
	err "version aliases differ"
	FAILS=$((FAILS + 1))
fi

info "help covers the command set"
out=$(podman exec nz-server nz --help 2>&1)
for cmd in run install uninstall reload list allow deny version; do
	expect_contains "help lists $cmd" "$out" "$cmd"
done
out=$(podman exec nz-server nz allow --help 2>&1)
expect_contains "sub help shows usage" "$out" "USAGE"

info "unified hint when the daemon is down"
podman exec nz-node-c sh -c 'pkill -f "nz run"' >/dev/null 2>&1
sleep 1
out=$(podman exec nz-node-c nz list 2>&1)
expect_contains "stopped daemon reports not running" "$out" "no daemon answers"
expect_contains "hint points at install" "$out" "nz install"
start_node_daemon node-c
sleep 3

finish
