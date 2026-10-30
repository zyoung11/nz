#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "removing containers"
for name in server node-a node-b node-c; do
	podman rm -f "n0-$name" >/dev/null 2>&1
done

info "removing network $NET"
podman network rm -f "$NET" >/dev/null 2>&1

rm -rf "$TESTDIR/cfg" "$TESTDIR/bin"
ok "stack removed"
