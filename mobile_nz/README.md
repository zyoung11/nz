# NZ Mobile Client

Android client for NZ overlay network.

## Technical Route

### Protocol

NZ uses a simple UDP-based protocol:

1. **Handshake** (4 messages):
   - Client → Server: `Hello` (name, desiredIP, clientNonce)
   - Server → Client: `HelloReply` (encrypted clientNonce, serverNonce)
   - Client → Server: `Confirm` (encrypted serverNonce)
   - Server → Client: `ConfirmReply` (encrypted VPN IP + netmask)

2. **Data Relay**:
   - Client → Server: `RelayData` (encrypted: srcIP + targetIP + packet)
   - Server → Client: `RelayData` (forwarded)

3. **Keepalive**: Every 5 seconds

### Crypto

- Key derivation: Argon2id (3 iterations, 64MB, 4 threads)
- Encryption: AES-256-GCM
- Global salt: `nz-v1-global-salt-16`

### VPN

- Android VpnService with TUN device
- `addDisallowedApplication` to prevent routing loop
- TUN reads/writes via FileInputStream/FileOutputStream
- VPN service runs in separate process (`:nzVpnBg`)
- Status detection via SharedPreferences
- Connecting/Disconnecting intermediate states with loading animation

## Development

### Prerequisites

- Flutter SDK 3.47.0+ (installed at `/opt/flutter/bin`)
- Android Studio + SDK (installed at `~/Android/Sdk`)
- Android NDK 30.0.15729638 (installed at `~/Android/Sdk/ndk/30.0.15729638`)
- Java 17 (OpenJDK)
- Bouncy Castle (for Argon2id)

### Build & Deploy

```bash
cd mobile_nz

# Full workflow: start emulator → build → install → launch
./dev.sh

# Individual steps
./dev.sh emu      # Start emulator only
./dev.sh build    # Build APK only
./dev.sh install  # Install APK only
./dev.sh launch   # Launch app only
```

### Manual Build

```bash
# Set environment
export PATH="$PATH:/opt/flutter/bin"
export QT_QPA_PLATFORM=xcb

# Build APK (arm64 only, ~19MB)
flutter build apk --release --target-platform android-arm64

# Build APK (all architectures, ~50MB)
flutter build apk --release

# Install on device
~/Android/Sdk/platform-tools/adb install -r build/app/outputs/flutter-apk/app-release.apk

# Launch
~/Android/Sdk/platform-tools/adb shell am start -n com.nz.mobile_nz/.MainActivity
```

### Debugging

```bash
# View all NZ logs
~/Android/Sdk/platform-tools/adb logcat -s NzClient NzVpnService

# View only errors
~/Android/Sdk/platform-tools/adb logcat -s NzClient NzVpnService -d | grep -i error

# View handshake progress
~/Android/Sdk/platform-tools/adb logcat -s NzClient -d | grep -i "handshake\|connected"

# View TUN operations
~/Android/Sdk/platform-tools/adb logcat -s NzVpnService -d | grep -i "tun"

# View received messages
~/Android/Sdk/platform-tools/adb logcat -s NzClient -d | grep "Received"
```

## Configuration

Node configuration stored in SharedPreferences:

```json
{
  "name": "my-phone",
  "password": "your-password",
  "domain": "vpn.example.com",
  "ip": 2
}
```

- `name`: Node identifier
- `password`: Shared network password (must match server)
- `domain`: Server address (IP or hostname)
- `ip`: Last octet of VPN IP (2-254)

## Features

### Connection
- VPN connection with automatic handshake
- Keepalive every 5 seconds
- Network change detection and auto-reconnect
- Always-on VPN support

### Battery Optimization
- VPN service runs in separate process (`:nzVpnBg`)
- Set as unmetered network to avoid battery optimization
- Exclude problematic apps (Android Auto, Chromecast, RCS)

### Node List
- Query server for node list (similar to `nz ls`)
- Display node name, IP, and status (online/offline)
- Highlight local node in blue
