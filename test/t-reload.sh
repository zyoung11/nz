#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "reload applies a new pinned ip"

nzs allow friend >/dev/null 2>&1
nzs allow friend/pc >/dev/null 2>&1
sleep 1
expect_eq "baseline ip before reload" "$(machine_ip_by_name pc)" "192.168.100.7"

info "pin change through reload"
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"nz-server\",\"port\":4399,\"ip\":9}" > /cfg/config.json'
podman exec nz-node-c nz reload >/dev/null 2>&1
sleep 3
expect_eq "reload moves the machine to the new pin" "$(machine_ip_by_name pc)" "192.168.100.9"
out=$(podman exec nz-node-c nz list 2>&1)
expect_contains "node reports the new vpn ip" "$out" "192.168.100.9"

info "removing the pin keeps the current ip"
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"nz-server\",\"port\":4399}" > /cfg/config.json'
podman exec nz-node-c nz reload >/dev/null 2>&1
sleep 3
expect_eq "ip reused without a pin" "$(machine_ip_by_name pc)" "192.168.100.9"

info "node rename through reload"
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pcrenamed\",\"domain\":\"nz-server\",\"port\":4399}" > /cfg/config.json'
podman exec nz-node-c nz reload >/dev/null 2>&1
sleep 3
out=$(podman exec nz-server nz list 2>&1)
expect_contains "rename applies through reload" "$out" "pcrenamed"

info "server rejects structural changes"
podman exec nz-server sh -c 'echo "{\"mode\":\"server\",\"port\":5000,\"subnet\":\"192.168.100.0/24\",\"admin\":{\"name\":\"me\",\"password\":\"adminpw\"}}" > /cfg/config.json'
out=$(podman exec nz-server nz reload 2>&1)
expect_contains "port change refused" "$out" "needs a service restart"
podman exec nz-server sh -c 'echo "{\"mode\":\"node\",\"user\":\"x\",\"password\":\"x\",\"name\":\"x\",\"domain\":\"x\"}" > /cfg/config.json'
out=$(podman exec nz-server nz reload 2>&1)
expect_contains "mode change refused" "$out" "needs a service reinstall"

info "server accepts admin password rotation"
podman exec nz-server sh -c 'echo "{\"mode\":\"server\",\"port\":4399,\"subnet\":\"192.168.100.0/24\",\"admin\":{\"name\":\"me\",\"password\":\"newadminpw\"}}" > /cfg/config.json'
out=$(podman exec nz-server nz reload 2>&1)
expect_contains "password rotation accepted" "$out" "config reloaded"
podman exec nz-server sh -c 'echo "{\"mode\":\"server\",\"port\":4399,\"subnet\":\"192.168.100.0/24\",\"admin\":{\"name\":\"me\",\"password\":\"adminpw\"}}" > /cfg/config.json'
podman exec nz-server nz reload >/dev/null 2>&1

info "restoring pc for later suites"
podman exec nz-node-c sh -c 'echo "{\"mode\":\"node\",\"user\":\"friend\",\"password\":\"friendpw\",\"name\":\"pc\",\"domain\":\"nz-server\",\"port\":4399,\"ip\":7}" > /cfg/config.json'
start_node_daemon node-c
sleep 3

finish
