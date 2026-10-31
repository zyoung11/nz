#!/usr/bin/env bash
set -u

TESTDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$TESTDIR")"
NET=nz-net
IMG=docker.io/library/ubuntu:24.04
BIN="$TESTDIR/bin/nz"
BIN_V1="$TESTDIR/bin/nz"

BLUE='\033[38;2;138;173;244m'
GREEN='\033[38;2;166;218;149m'
YELLOW='\033[38;2;238;212;159m'
RED='\033[38;2;237;135;150m'
RESET='\033[0m'

info() { printf "${BLUE}[i] %s${RESET}\n" "$*"; }
ok()   { printf "${GREEN}[+] %s${RESET}\n" "$*"; }
warn() { printf "${YELLOW}[!] %s${RESET}\n" "$*"; }
err()  { printf "${RED}[-] %s${RESET}\n" "$*"; }

FAILS=0

expect_eq() {
	local desc="$1" actual="$2" want="$3"
	if [ "$actual" = "$want" ]; then
		ok "$desc"
	else
		err "$desc: got [$actual] want [$want]"
		FAILS=$((FAILS + 1))
	fi
}

expect_contains() {
	local desc="$1" haystack="$2" needle="$3"
	if [[ "$haystack" == *"$needle"* ]]; then
		ok "$desc"
	else
		err "$desc: missing [$needle]"
		err "  actual: $(echo "$haystack" | tr '\n' '|' | head -c 200)"
		FAILS=$((FAILS + 1))
	fi
}

expect_not_contains() {
	local desc="$1" haystack="$2" needle="$3"
	if [[ "$haystack" != *"$needle"* ]]; then
		ok "$desc"
	else
		err "$desc: unexpected [$needle]"
		FAILS=$((FAILS + 1))
	fi
}

nzs() { podman exec nz-server nz "$@"; }

nzc() {
	local container="$1"
	shift
	podman exec "nz-$container" nz "$@"
}

start_node_daemon() {
	local container="$1"
	podman exec "nz-$container" pkill -f "nz run" >/dev/null 2>&1
	sleep 1
	podman exec "nz-$container" sh -c 'rm -f /usr/local/bin/nz.log'
	podman exec -d "nz-$container" sh -c 'nz run --config /cfg/config.json --log 2>> /usr/local/bin/nz.log'
}

container_log() {
	podman exec "nz-$1" sh -c 'cat /usr/local/bin/nz.log 2>/dev/null'
}

wait_joined() {
	local container="$1" timeout="${2:-10}"
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		if container_log "$container" | grep -q "joined as"; then
			ok "$container joined"
			return 0
		fi
		sleep 1
	done
	err "$container did not join in ${timeout}s"
	FAILS=$((FAILS + 1))
	return 1
}

wait_pending() {
	local name="$1" timeout="${2:-20}"
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		if [[ "$(podman exec nz-server nz list --pending 2>&1)" == *"$name"* ]]; then
			return 0
		fi
		sleep 1
	done
	err "machine $name did not appear in pending"
	FAILS=$((FAILS + 1))
	return 1
}

machine_ip() {
	nzs list | grep -oE "192\.168\.200\.[0-9]+" | sed -n "${1}p"
}

reset_grants() {
	podman cp nz-server:/cfg/state.json /tmp/nz-state.json 2>/dev/null
	python3 - <<'PYEOF2'
import json
d = json.load(open("/tmp/nz-state.json"))
d["grants"] = []
for m in d.get("machines", []):
    if m.get("status") == "blocked":
        m["status"] = "active"
for u in d.get("users", []):
    if u.get("status") == "blocked":
        u["status"] = "active"
json.dump(d, open("/tmp/nz-state.json", "w"), indent=2)
PYEOF2
	podman cp /tmp/nz-state.json nz-server:/cfg/state.json 2>/dev/null
	podman exec nz-server pkill -f "nz run" >/dev/null 2>&1
	sleep 1
	podman exec -d nz-server sh -c 'nz run --config /cfg/config.json --log 2>> /usr/local/bin/nz.log'
	sleep 6
}

machine_ip_of() {
	nzs list | sed 's/\x1b\[[0-9;]*m//g' | awk -v user="$1" -v name="$2" -F'│' '{
		gsub(/^ +| +$/, "", $2)
		gsub(/^ +| +$/, "", $3)
		if ($2 == user && $3 == name) {
			for (i = 1; i <= NF; i++) {
				if ($i ~ /192\.168\./) {
					gsub(/ /, "", $i)
					print $i
					exit
				}
			}
		}
	}'
}

machine_ip_by_name() {
	nzs list | sed 's/\x1b\[[0-9;]*m//g' | awk -v name="$1" -F'│' '{
		gsub(/^ +| +$/, "", $2)
		if ($2 == name) {
			for (i = 1; i <= NF; i++) {
				if ($i ~ /192\.168\./) {
					gsub(/ /, "", $i)
					print $i
					exit
				}
			}
		}
	}'
}

ping_vpn() {
	local container="$1" target="$2"
	podman exec "nz-$container" ping -c 2 -W 2 "$target" >/dev/null 2>&1
}

finish() {
	if [ "$FAILS" -eq 0 ]; then
		ok "all checks passed"
		exit 0
	fi
	err "$FAILS check(s) failed"
	exit 1
}
