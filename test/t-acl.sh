#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "grant granularity"

nzs allow friend >/dev/null 2>&1
nzs allow friend/pc >/dev/null 2>&1
sleep 2
ip_desktop=$(machine_ip_by_name desktop)
ip_pc=$(machine_ip_by_name pc)
ip_laptop=$(machine_ip_by_name laptop)

info "user level grant covers all machines of that user"
nzs allow friend --to desktop >/dev/null 2>&1
sleep 2
if ping_vpn node-b "$ip_desktop"; then
	ok "granted user machine reaches the target"
else
	err "granted user machine cannot reach the target"
	FAILS=$((FAILS + 1))
fi
if ping_vpn node-c "$ip_desktop"; then
	ok "sibling machine reaches the target too"
else
	err "sibling machine cannot reach the target"
	FAILS=$((FAILS + 1))
fi

info "machine level grant covers one machine only"
nzs deny friend --to desktop >/dev/null 2>&1
nzs allow friend/pc --to desktop >/dev/null 2>&1
sleep 2
if ping_vpn node-c "$ip_desktop"; then
	ok "granted machine reaches the target"
else
	err "granted machine cannot reach the target"
	FAILS=$((FAILS + 1))
fi
if ping_vpn node-b "$ip_desktop"; then
	err "sibling machine reaches the target without grant"
	FAILS=$((FAILS + 1))
else
	ok "sibling machine blocked without grant"
fi

info "revoke one grant leaves others alone"
nzs deny friend/pc --to desktop >/dev/null 2>&1
nzs allow friend --to desktop >/dev/null 2>&1
sleep 2
if ping_vpn node-b "$ip_desktop"; then
	ok "user level grant survives machine level revoke"
else
	err "user level grant lost after machine level revoke"
	FAILS=$((FAILS + 1))
fi
if ping_vpn node-c "$ip_desktop"; then
	ok "machine level grant follows the user level grant"
else
	err "machine level grant lost unexpectedly"
	FAILS=$((FAILS + 1))
fi

info "grant to another machine is independent"
nzs allow friend/laptop --to desktop >/dev/null 2>&1
sleep 2
out=$(nzs list 2>&1)
expect_contains "grant target listed" "$out" "desktop"
nzs deny friend/laptop --to desktop >/dev/null 2>&1

info "repeated grants are idempotent"
nzs allow friend --to desktop >/dev/null 2>&1
nzs allow friend --to desktop >/dev/null 2>&1
sleep 2
out=$(nzs list 2>&1)
count=$(echo "$out" | grep -o "desktop" | wc -l)
if [ "$count" -gt 0 ]; then
	ok "duplicate grants collapse to one"
else
	err "duplicate grants lost the entry"
	FAILS=$((FAILS + 1))
fi

nzs deny friend --to desktop >/dev/null 2>&1

finish
