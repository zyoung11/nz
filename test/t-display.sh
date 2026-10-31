#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "cli display semantics"

n0s allow friend >/dev/null 2>&1
n0s allow friend/pc >/dev/null 2>&1
sleep 2

info "local row follows the querying machine"
out=$(podman exec n0-server n0 list 2>&1)
echo "$out" | grep -q "server" && expect_contains "server view marks server local" "$out" "local"
out=$(podman exec n0-node-a n0 list 2>&1)
expect_contains "node view marks itself local" "$out" "local"
expect_contains "node view shows its own user group" "$out" "me"

info "status words are consistent"
out=$(podman exec n0-server n0 list --pending 2>&1)
expect_contains "pending view lists pending" "$out" "pending"

info "aliases match the real command"
out1=$(podman exec n0-server n0 version 2>&1)
out2=$(podman exec n0-server n0 -v 2>&1)
out3=$(podman exec n0-server n0 --version 2>&1)
if [ "$out1" = "$out2" ] && [ "$out1" = "$out3" ]; then
	ok "version aliases match"
else
	err "version aliases differ"
	FAILS=$((FAILS + 1))
fi

info "help covers the command set"
out=$(podman exec n0-server n0 --help 2>&1)
for cmd in run install uninstall reload list allow deny version; do
	expect_contains "help lists $cmd" "$out" "$cmd"
done
out=$(podman exec n0-server n0 allow --help 2>&1)
expect_contains "sub help shows usage" "$out" "USAGE"

info "unified hint when the daemon is down"
podman exec n0-node-c sh -c 'pkill -f "n0 run"' >/dev/null 2>&1
sleep 1
out=$(podman exec n0-node-c n0 list 2>&1)
expect_contains "stopped daemon reports not running" "$out" "no daemon answers"
expect_contains "hint points at install" "$out" "n0 install"
start_node_daemon node-c
sleep 3

finish
