# nz

A compact overlay network tool for personal use — connect your own devices into a virtual LAN.
Single binary, config-file driven. Password-only auth, NAT hole-punching with relay fallback.
Runs on **Linux** and **Windows**.

Forked and simplified from [Slack's Nebula](https://github.com/slackhq/nebula).

## Prerequisites

The server must have its **UDP port open** (default `4242`).

- **Cloud VPS**: open the port in your security group / firewall rules
- **Home server**: forward the port on your router

Nodes don't need any open ports — they initiate outbound connections.

## How It Works

One server, many nodes — all your own devices. Server gets `192.168.100.1`. Each node gets a sequential IP (`.2`, `.3`, ...). All traffic encrypted with AES-256-GCM key derived from your password via Argon2id.

Nodes prefer direct P2P connections (UDP hole-punching). When NAT prevents direct connection, traffic falls back to server relay.

## Install

Download the latest binary from [GitHub Releases](https://github.com/zyoung11/nz/releases).

## Quick Start

Create `config.json` next to the binary:

**Server:**
```json
{"mode":"server","password":"mysecret","name":"my-vps"}
```

**Node:**
```json
{"mode":"node","password":"mysecret","name":"laptop","domain":"my-server.com"}
```

### Linux

```bash
sudo ./nz install --config config.json   # register systemd service (auto-start)
./nz ls                                   # list all nodes
./nz run                                  # foreground (testing)
sudo ./nz uninstall                       # stop and remove
```

### Windows

Double-click `nz.exe` to launch the system tray app. Or use CLI:

```powershell
.\nz.exe install --config config.json     # register auto-start (Task Scheduler)
.\nz.exe ls                                # list all nodes
.\nz.exe run                               # foreground (testing)
.\nz.exe uninstall                         # stop and remove
```

The tray icon shows online/offline status, opens a web dashboard, and lets you toggle auto-start.

Nodes can now reach each other on `192.168.100.x` — ping, SSH, or any TCP/UDP service.

## CLI

```
./nz install  --config <path>   Register auto-start
./nz uninstall                  Stop and remove
./nz ls                         List all nodes with status
./nz run                        Run in foreground
```

`ls` output shows a Unicode table with each node's name, VPN IP, and status:
- `online` — connected, heartbeats active
- `idle` — connected, no recent heartbeat
- `probing` — server testing if node is alive
- `offline` — not connected

The local machine's row is highlighted.

## Configuration

### Server (all fields)

```json
{"mode":"server","password":"mysecret","name":"my-vps","port":4242,"tun":"nz0"}
```

### Node (all fields)

```json
{"mode":"node","password":"mysecret","name":"laptop","domain":"my-server.com","ip":5,"route":"auto","port":4242,"tun":"nz0"}
```

### Fields

| Field | Mode | Required | Default | Description |
|---|---|---|---|---|
| `mode` | both | yes | — | `"server"` or `"node"` |
| `password` | both | yes | — | Shared secret |
| `name` | both | no | `"server"` | Device identifier (unique per node) |
| `domain` | node | yes | — | Server address or hostname |
| `ip` | node | no | auto | Request specific VPN IP (2–254) |
| `route` | node | no | `auto` | `auto` / `p2p` / `relay` |
| `port` | both | no | `4242` | UDP port (must match across all peers) |
| `tun` | both | no | `nz0` | TUN device name |

## Build

```bash
# Linux
go build -ldflags="-s -w" -o nz .

# Windows (needs rsrc for icon + UAC manifest)
rsrc -manifest nz.exe.manifest -ico nz.ico -o rsrc_windows_amd64.syso
go build -ldflags="-s -w" -o nz.exe .
```

## Network

`192.168.100.0/24`. Server fixed at `.1`, nodes allocated `.2` through `.254`.

## Credits

Forked and simplified from [Slack's Nebula](https://github.com/slackhq/nebula). Stripped down from ~35,000 lines to ~2,500 for personal use — removed PKI/certificates, firewall, SSH, DNS, stats, and YAML config.

## License

MIT
