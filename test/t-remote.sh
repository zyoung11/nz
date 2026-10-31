#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "remote management from an admin node"

podman exec nz-server nz allow friend >/dev/null 2>&1
podman exec nz-server nz allow friend/laptop >/dev/null 2>&1
podman exec nz-server nz deny friend/pc >/dev/null 2>&1
for _ in $(seq 1 20); do
	[[ "$(podman exec nz-server nz list --pending 2>&1)" == *"pc"* ]] && break
	sleep 1
done

out=$(podman exec nz-node-a nz list 2>&1)
expect_contains "admin node reads the user table" "$out" "friend"

out=$(podman exec nz-node-a nz allow friend/pc 2>&1)
expect_contains "admin node approves a machine" "$out" "done"
sleep 3
out=$(podman exec nz-server nz list 2>&1)
expect_contains "approval took effect on the server" "$out" "pc"

out=$(podman exec nz-node-a nz deny friend/pc 2>&1)
expect_contains "admin node blocks a machine" "$out" "done"
sleep 1
out=$(podman exec nz-server nz list 2>&1)
expect_contains "block took effect on the server" "$out" "blocked"

out=$(podman exec nz-node-a nz allow friend/pc 2>&1)
expect_contains "admin node unblocks a machine" "$out" "done"

out=$(podman exec nz-node-b nz allow friend/pc 2>&1)
expect_contains "plain node cannot manage" "$out" "only admin machines"

finish
