#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

TOTAL_FAILS=0

info "rebuilding stack"
bash "$TESTDIR/podman-down.sh" >/dev/null 2>&1
bash "$TESTDIR/podman-up.sh" >/dev/null 2>&1 || {
	err "stack startup failed"
	exit 1
}
sleep 2

for t in t-join t-ip t-p2p t-relay t-block t-trust t-forge t-rotate t-ui t-coexist; do
	info "===== $t ====="
	if bash "$TESTDIR/$t.sh"; then
		ok "$t passed"
	else
		err "$t failed"
		TOTAL_FAILS=$((TOTAL_FAILS + 1))
	fi
done

echo
if [ "$TOTAL_FAILS" -eq 0 ]; then
	ok "all suites passed"
else
	err "$TOTAL_FAILS suite(s) failed"
fi
exit "$TOTAL_FAILS"
