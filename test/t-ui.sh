#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "cli output and permission boundaries"

out=$(podman exec n0-node-b n0 status 2>&1)
expect_contains "node status shows connection" "$out" "connection"
expect_not_contains "node status hides user stats" "$out" "users"

out=$(podman exec n0-server n0 status 2>&1)
expect_contains "server status shows user stats" "$out" "users"

out=$(podman exec n0-node-b n0 list 2>&1)
expect_contains "list opens with the server row" "$out" "server"
expect_contains "list marks the server local" "$out" "local"

podman exec n0-node-b useradd -m tester >/dev/null 2>&1
out=$(podman exec --user tester n0-node-b n0 allow friend 2>&1)
expect_contains "write command needs root" "$out" "needs root"
out=$(podman exec --user tester n0-node-b n0 status 2>&1)
expect_contains "read command works without root" "$out" "running"

out=$(podman exec n0-node-b n0 users 2>&1)
expect_contains "users refused on node" "$out" "only available on the server"

out=$(podman exec n0-node-b sh -c 'cp /cfg/config.json /cfg/config.bak; echo bad > /cfg/config.json; n0 reload; mv /cfg/config.bak /cfg/config.json' 2>&1)
expect_contains "reload keeps old config on parse error" "$out" "keep running with old config"

out=$(timeout 8 podman exec n0-node-b n0 run --config /cfg/config.json 2>&1)
expect_contains "second run instance refused" "$out" "already running"

finish
