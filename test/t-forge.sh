#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "forged source packets are dropped"

if (cd "$REPO" && go test ./internal/server -run "TestForgedSourceDropped" >/dev/null 2>&1); then
	ok "forged source dropped"
else
	err "forged source test failed"
	FAILS=$((FAILS + 1))
fi

finish
