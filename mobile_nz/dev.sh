#!/bin/bash
set -e

export PATH="$PATH:/opt/flutter/bin"
export QT_QPA_PLATFORM=xcb

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
ADB="$HOME/Android/Sdk/platform-tools/adb"
EMULATOR="$HOME/Android/Sdk/emulator/emulator"
AVD="Pixel_10"
APK="$PROJECT_DIR/build/app/outputs/flutter-apk/app-debug.apk"
PACKAGE="com.nz.mobile_nz"
ACTIVITY="$PACKAGE/.MainActivity"

start_emulator() {
    if $ADB devices 2>/dev/null | grep -q "emulator.*device"; then
        if $ADB shell getprop sys.boot_completed 2>/dev/null | grep -q "1"; then
            echo "[*] Emulator already running"
            return
        fi
    fi
    echo "[*] Starting emulator..."
    nohup $EMULATOR -avd $AVD -gpu host > /tmp/emulator.log 2>&1 &
    echo "[*] Waiting for emulator..."
    for i in $(seq 1 60); do
        if $ADB devices 2>/dev/null | grep -q "emulator.*device"; then
            if $ADB shell getprop sys.boot_completed 2>/dev/null | grep -q "1"; then
                echo "[+] Emulator ready"
                return
            fi
        fi
        sleep 2
    done
    echo "[-] Emulator startup timeout"
    exit 1
}

build_apk() {
    echo "[*] Building APK..."
    cd "$PROJECT_DIR"
    flutter build apk --debug
    echo "[+] APK built"
}

install_apk() {
    echo "[*] Installing APK..."
    $ADB install -r "$APK"
    echo "[+] Installed"
}

launch_app() {
    echo "[*] Launching app..."
    $ADB shell am start -n "$ACTIVITY"
    echo "[+] App launched"
}

case "${1:-run}" in
    build)
        build_apk
        ;;
    install)
        install_apk
        ;;
    launch)
        launch_app
        ;;
    emu)
        start_emulator
        ;;
    run)
        start_emulator
        build_apk
        install_apk
        launch_app
        ;;
    *)
        echo "Usage: $0 {run|build|install|launch|emu}"
        echo "  run     - Full workflow (default): start emulator → build → install → launch"
        echo "  build   - Build APK only"
        echo "  install - Install APK only"
        echo "  launch  - Launch app only"
        echo "  emu     - Start emulator only"
        ;;
esac
