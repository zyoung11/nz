#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "cli output and permission boundaries"

out=$(podman exec nz-node-b nz list 2>&1)
expect_contains "node list shows connection" "$out" "connection"
expect_contains "node list groups by user" "$out" "Machines"

out=$(podman exec nz-server nz list 2>&1)
expect_contains "server list shows stats" "$out" "machines"
expect_contains "list opens with the server row" "$out" "server"
expect_contains "list marks the local row" "$out" "local"

podman exec nz-node-b useradd -m tester >/dev/null 2>&1
out=$(podman exec --user tester nz-node-b nz allow friend 2>&1)
expect_contains "write command needs root" "$out" "needs root"
out=$(podman exec --user tester nz-node-b nz list 2>&1)
expect_contains "read command works without root" "$out" "version"

out=$(podman exec nz-node-b sh -c 'cp /cfg/config.json /cfg/config.bak; echo bad > /cfg/config.json; nz reload; mv /cfg/config.bak /cfg/config.json' 2>&1)
expect_contains "reload keeps old config on parse error" "$out" "keep running with old config"

out=$(timeout 8 podman exec nz-node-b nz run --config /cfg/config.json 2>&1)
expect_contains "second run instance refused" "$out" "already running"

finish
