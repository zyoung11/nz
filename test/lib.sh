#!/usr/bin/env bash
set -u

TESTDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$TESTDIR")"
NET=n0-net
IMG=docker.io/library/ubuntu:24.04
BIN="$TESTDIR/bin/n0"
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

n0s() { podman exec n0-server n0 "$@"; }

n0c() {
	local container="$1"
	shift
	podman exec "n0-$container" n0 "$@"
}

start_node_daemon() {
	local container="$1"
	podman exec "n0-$container" pkill -f "n0 run" >/dev/null 2>&1
	sleep 1
	podman exec -d "n0-$container" n0 run --config /cfg/config.json --log
}

container_log() {
	podman exec "n0-$1" sh -c 'cat /usr/local/bin/n0.log 2>/dev/null'
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

machine_ip() {
	n0s list | grep -oE "192\.168\.200\.[0-9]+" | sed -n "${1}p"
}

machine_ip_by_name() {
	n0s users | grep "$1" | grep -oE "192\.168\.200\.[0-9]+" | head -1
}

ping_vpn() {
	local container="$1" target="$2"
	podman exec "n0-$container" ping -c 2 -W 2 "$target" >/dev/null 2>&1
}

finish() {
	if [ "$FAILS" -eq 0 ]; then
		ok "all checks passed"
		exit 0
	fi
	err "$FAILS check(s) failed"
	exit 1
}
