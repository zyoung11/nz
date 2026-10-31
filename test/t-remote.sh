#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "remote management from an admin node"

podman exec n0-server n0 allow friend >/dev/null 2>&1
podman exec n0-server n0 allow friend/laptop >/dev/null 2>&1
podman exec n0-server n0 deny friend/pc >/dev/null 2>&1
for _ in $(seq 1 20); do
	[[ "$(podman exec n0-server n0 list --pending 2>&1)" == *"pc"* ]] && break
	sleep 1
done

out=$(podman exec n0-node-a n0 list 2>&1)
expect_contains "admin node reads the user table" "$out" "friend"

out=$(podman exec n0-node-a n0 allow friend/pc 2>&1)
expect_contains "admin node approves a machine" "$out" "done"
sleep 3
out=$(podman exec n0-server n0 list 2>&1)
expect_contains "approval took effect on the server" "$out" "pc"

out=$(podman exec n0-node-a n0 deny friend/pc 2>&1)
expect_contains "admin node blocks a machine" "$out" "done"
sleep 1
out=$(podman exec n0-server n0 list 2>&1)
expect_contains "block took effect on the server" "$out" "blocked"

out=$(podman exec n0-node-a n0 allow friend/pc 2>&1)
expect_contains "admin node unblocks a machine" "$out" "done"

out=$(podman exec n0-node-b n0 allow friend/pc 2>&1)
expect_contains "plain node cannot manage" "$out" "only admin machines"

finish
