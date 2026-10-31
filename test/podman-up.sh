#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "building nz"
mkdir -p "$TESTDIR/bin"
(cd "$REPO" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/nz) || {
	err "build failed"
	exit 1
}

info "creating network $NET"
podman network exists "$NET" || podman network create "$NET" >/dev/null

start_container() {
	local name="$1" cfg="$2"
	local dir="$TESTDIR/cfg/$name"
	mkdir -p "$dir"
	printf '%s\n' "$cfg" >"$dir/config.json"
	podman rm -f "nz-$name" >/dev/null 2>&1
	podman run -d --name "nz-$name" --network "$NET" \
		--cap-add NET_ADMIN --cap-add NET_RAW --device /dev/net/tun \
		-v "$dir:/cfg:Z" "$IMG" sleep infinity >/dev/null
	podman cp "$BIN" "nz-$name:/usr/local/bin/nz" >/dev/null
	podman exec "nz-$name" sh -c 'command -v ping >/dev/null || (apt-get update -qq && apt-get install -y -qq iputils-ping)'
}

info "starting containers"
start_container server '{"mode":"server","admin":{"name":"me","password":"adminpw"},"port":4399}'
start_container node-a '{"mode":"node","user":"me","password":"adminpw","name":"desktop","domain":"nz-server","port":4399}'
start_container node-b '{"mode":"node","user":"friend","password":"friendpw","name":"laptop","domain":"nz-server","port":4399}'
start_container node-c '{"mode":"node","user":"friend","password":"friendpw","name":"pc","domain":"nz-server","port":4399}'

info "starting daemons"
podman exec -d nz-server nz run --config /cfg/config.json --log
sleep 1
podman exec -d nz-node-a nz run --config /cfg/config.json --log
podman exec -d nz-node-b nz run --config /cfg/config.json --log
podman exec -d nz-node-c nz run --config /cfg/config.json --log

sleep 1
ok "stack up: server node-a node-b node-c"
