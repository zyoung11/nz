#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "building n0"
mkdir -p "$TESTDIR/bin"
(cd "$REPO" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/n0) || {
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
	podman rm -f "n0-$name" >/dev/null 2>&1
	podman run -d --name "n0-$name" --network "$NET" \
		--cap-add NET_ADMIN --cap-add NET_RAW --device /dev/net/tun \
		-v "$dir:/cfg:Z" "$IMG" sleep infinity >/dev/null
	podman cp "$BIN" "n0-$name:/usr/local/bin/n0" >/dev/null
	podman exec "n0-$name" sh -c 'command -v ping >/dev/null || (apt-get update -qq && apt-get install -y -qq iputils-ping)'
}

info "starting containers"
start_container server '{"mode":"server","admin":{"name":"me","password":"adminpw"},"port":4399}'
start_container node-a '{"mode":"node","user":"me","password":"adminpw","name":"desktop","domain":"n0-server","port":4399}'
start_container node-b '{"mode":"node","user":"friend","password":"friendpw","name":"laptop","domain":"n0-server","port":4399}'
start_container node-c '{"mode":"node","user":"friend","password":"friendpw","name":"pc","domain":"n0-server","port":4399}'

info "starting daemons"
podman exec -d n0-server n0 run --config /cfg/config.json
sleep 1
podman exec -d n0-node-a n0 run --config /cfg/config.json
podman exec -d n0-node-b n0 run --config /cfg/config.json
podman exec -d n0-node-c n0 run --config /cfg/config.json

sleep 1
ok "stack up: server node-a node-b node-c"
