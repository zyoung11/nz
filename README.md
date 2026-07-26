# NetZero

A compact overlay network tool for personal use — connect your own devices into a virtual LAN.
Single binary, password-only auth, NAT hole-punching with relay fallback.
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

Download the latest binary from [GitHub Releases](https://github.com/zyoung11/NetZero/releases).

Linux: `netzero-linux` | Windows: `netzero-win.exe`

## Setup

Create `config.json` next to the binary, then register as a service for auto-start on boot.

**Server:**
```json
{"mode":"server","password":"mysecret","name":"my-vps"}
```

**Node:**
```json
{"mode":"node","password":"mysecret","name":"laptop","domain":"my-server.com"}
```

```bash
# Install and start (requires root / admin)
sudo ./netzero install --config config.json

# Check status
sudo ./netzero status

# Stop and remove service
sudo ./netzero uninstall
```

Linux uses systemd. Windows uses SCM (wintun.dll embedded in binary, no separate driver install needed).

## Testing (Foreground)

```bash
# Server
./netzero --server --password mysecret

# Node
./netzero --node --name laptop --domain my-server.com --password mysecret
```

Now nodes can reach each other on `192.168.100.x`.

## Configuration

### Server (example with all fields)

```json
{
    "mode":     "server",
    "password": "mysecret",
    "name":     "my-vps",
    "port":     6969,
    "tun":      "netzero"
}
```

### Node (example with all fields)

```json
{
    "mode":     "node",
    "password": "mysecret",
    "name":     "laptop",
    "domain":   "my-server.com",
    "ip":       50,
    "route":    "relay",
    "port":     6969,
    "tun":      "netzero"
}
```

### Fields

| Field | Mode | Required | Default | Description |
|---|---|---|---|---|
| `mode` | both | yes | — | `"server"` or `"node"` |
| `password` | both | yes | — | Shared secret |
| `name` | both | no | `"server"` | Device identifier (must be unique per node) |
| `domain` | node | yes | — | Server address or hostname |
| `ip` | node | no | auto | Request specific VPN IP (2-254) |
| `route` | node | no | `auto` | `auto` / `p2p` / `relay` |
| `port` | both | no | `4242` | UDP port (must match across all peers) |
| `tun` | both | no | `nz0` | TUN device name |

## CLI

```
netzero --server --password <password>
netzero --node --name <name> --domain <addr> --password <password>
netzero --config <path>

netzero install  --config <path>
netzero uninstall
netzero status
```

## Build

```bash
go build -ldflags="-s -w" .
```

## Network

`192.168.100.0/24`. Server fixed at `.1`, nodes allocated `.2` through `.254`.

## Credits

Forked and simplified from [Slack's Nebula](https://github.com/slackhq/nebula). Stripped down from ~35,000 lines to ~2,300 for personal use — removed PKI/certificates, firewall, SSH, DNS, stats, and YAML config.

## License

MIT
