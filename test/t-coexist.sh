#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "legacy nz and nz run side by side"

info "building legacy nz from main branch"
WORKTREE="$TESTDIR/.v1-src"
git -C "$REPO" worktree remove --force "$WORKTREE" >/dev/null 2>&1
git -C "$REPO" worktree add "$WORKTREE" main >/dev/null 2>&1 || {
	err "cannot create worktree from main"
	finish
}
(cd "$WORKTREE" && CGO_ENABLED=0 go build -o "$BIN_V1" .) || {
	err "legacy build failed"
	git -C "$REPO" worktree remove --force "$WORKTREE" >/dev/null 2>&1
	finish
}
git -C "$REPO" worktree remove --force "$WORKTREE" >/dev/null 2>&1

info "starting legacy nz server in node-a container"
podman exec nz-node-a mkdir -p /opt/legacy
podman cp "$BIN_V1" nz-node-a:/opt/legacy/nz >/dev/null
printf '%s\n' '{"mode":"server","password":"legacy","port":4242}' | podman exec -i nz-node-a sh -c 'cat > /opt/legacy/config.json'
podman exec -d nz-node-a sh -c 'cd /opt/legacy && ./nz run > /cfg/nz.log 2>&1'
sleep 2

if podman exec nz-node-a pgrep -f "/opt/legacy" >/dev/null 2>&1; then
	ok "legacy nz running"
else
	err "legacy nz not running: $(podman exec nz-node-a sh -c 'tail -3 /cfg/nz.log 2>/dev/null')"
	FAILS=$((FAILS + 1))
fi

out=$(nzc node-a list 2>&1)
expect_contains "nz still healthy next to nz" "$out" "running"

out=$(container_log node-a)
expect_contains "nz tunnel unaffected" "$out" "joined as"

podman exec nz-node-a pkill -f "/opt/legacy" >/dev/null 2>&1
ok "legacy nz stopped"

finish
