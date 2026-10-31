# nz

nz is a personal overlay network tool. One server, many machines, joined into one virtual LAN.

## Build

```bash
go build -ldflags="-s -w" -o nz ./cmd/nz
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o nz.exe ./cmd/nz
```

## Quick Start

Server `config.json`:

```json
{
  "mode": "server",
  "port": 4399,
  "subnet": "192.168.100.0/24",
  "admin": {"name": "me", "password": "strong-password"}
}
```

Node `config.json`:

```json
{
  "mode": "node",
  "user": "me",
  "password": "strong-password",
  "name": "laptop",
  "domain": "vps.example.com",
  "port": 4399
}
```

Deploy steps:

1. Put `nz` and `config.json` in one directory.
2. On the server run `sudo nz install --config config.json`.
3. On your machines run `sudo nz install --config config.json`.
4. The first start generates the machine key and joins the network.

## Configuration

| Field | Mode | Required | Meaning |
| --- | --- | --- | --- |
| `mode` | all | yes | `server` or `node` |
| `port` | all | no | default `4399` |
| `subnet` | server | no | default `192.168.100.0/24` |
| `admin` | server | yes | admin account with `name` and `password` |
| `user` | node | yes | owning user name |
| `password` | node | yes | user password |
| `name` | node | yes | machine name, unique per user |
| `domain` | node | yes | server address, host name or IP |
| `ip` | node | no | pinned VPN host number from 2 to 254 |

The server reads `admin` as the administrator account. Machines of the admin user are approved automatically.

A node stores only `Argon2id(password, salt)` in `state.json`. Neither side keeps the password in plain text.

`ip` applies at first allocation only. Reconnects always reuse the address. A pinned address owned by another machine makes approval fail with `ip ... already used by ...`. An address is bound to a machine key and cannot be taken over.

## Hot Reload Rules

`nz reload` handles fields in two ways.

On a node every field takes effect. The tunnel restarts with the new config, so `user`, `password`, `name`, `domain` and `ip` changes are usable at once.

On the server only `admin.password` is hot reloaded. `port`, `subnet`, `admin.name` and `mode` are structural. Reload refuses them and asks for a service reinstall.

## Commands

| Command | Meaning |
| --- | --- |
| `nz run --config <path>` | run in foreground, for debugging |
| `nz install --config <path>` | start the daemon and enable auto-start |
| `nz uninstall` | stop the daemon and disable auto-start |
| `nz reload --config <path>` | validate config and apply it |
| `nz list [--pending]` | status line plus the user and machine tables |
| `nz ls [--pending]` | short form of `list` |
| `nz allow <target> [--to <machine>]` | approve or unblock or authorize |
| `nz deny <target> [--to <machine>]` | reject or block or revoke |
| `nz version` | show version |

Aliases: `version` also accepts `-v`, `-V` and `--version`. Help accepts `help`, `-h` and `--help`, and every subcommand does too.

Target syntax: `friend` is a user, `friend/laptop` is a machine. `--to` picks the machine being authorized. Without `--to` the target is the machine running the command.

`allow` and `deny` act on the current state of the target:

| Target state | `allow` | `deny` |
| --- | --- | --- |
| pending | approve and allocate an IP | delete the request |
| blocked | unblock | keep blocked |
| active | add a grant toward the target machine | block and revoke grants |

## Join Flow

A friend machine needs approval before it can join.

1. The friend writes a node `config.json` with `user` and `password`.
2. The friend runs `sudo nz install --config config.json`.
3. The machine generates its key and sends a request.
4. You run `nz list --pending` to see the request.
5. You run `nz allow friend` to approve the user.
6. You run `nz allow friend/laptop` to approve the machine. The server pushes the approval.

The machine joins at once. A lost push is retried every 5 seconds.

## Access Control

| Source | Target | Default |
| --- | --- | --- |
| admin machines | any machine | allowed, P2P allowed |
| same user machines | same user machines | allowed, P2P allowed |
| plain user machines | admin machines | denied, grants open it |
| user A machines | user B machines | denied |

A grant is a pair: who may actively call which machine. `nz allow friend` run on laptop authorizes friend to call laptop. `--to homelab` picks another machine. A user level grant covers all machines of that user, a machine level grant covers one machine.

A pair with one direction forced uses relay. The caller may start a call, the other side may only answer. A TCP SYN or an ICMP request is a call start, anything else is an answer.

## Security

1. Every machine has its own key pair, registered as a public key. The machine name is a label, the identity is the key.
2. Data is end to end encrypted. Key agreement uses elliptic curve Diffie-Hellman, one session key per machine pair.
3. The routing header is signed by the machine, so the server applies policy on the header without reading the payload.
4. The server allocates VPN addresses and binds them to machine keys.
5. Relay packets are identified by the signed identity. The claimed source address is never trusted.
6. Grants and blocks use keys, so renaming a machine changes nothing.

## Service Integration

On Windows `nz install` registers a Windows service. It starts at boot before user logon. The failure recovery policy restarts the process with a delay up to 30 seconds.

On Linux `nz install` writes a systemd unit with `Restart=always`. Without systemd run `nz start` with any init.

## Runtime Files

The program directory keeps only two files.

| Artifact | Location | Note |
| --- | --- | --- |
| `config.json` | program directory | written by you |
| `state.json` | program directory | written by the program, mode 0600 |
| admin socket | `/tmp/nz.sock` | removed on stop, Windows uses a named pipe |
| log | `nz.log` | only with `--log` |
| TUN device | kernel | created after joining |

`state.json` holds the machine key pair, the cached server keys, the user table, the machine table and the grant list.
