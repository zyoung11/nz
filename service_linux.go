//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceName = "nz"
const systemdDir = "/etc/systemd/system"

func installService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install requires root (use sudo)")
	}

	args, err := serviceArgs()
	if err != nil {
		return err
	}

	unitPath := filepath.Join(systemdDir, serviceName+".service")

	unit := fmt.Sprintf(`[Unit]
Description=nz overlay network
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, strings.Join(args, " "))

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("failed to write unit file: %w", err)
	}

	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload failed: %w (%s)", err, out)
	}
	if out, err := exec.Command("systemctl", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enable failed: %w (%s)", err, out)
	}
	if out, err := exec.Command("systemctl", "restart", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("start failed: %w (%s)", err, out)
	}

	logSuccess("service installed and started")
	return nil
}

func uninstallService() error {
	unitPath := filepath.Join(systemdDir, serviceName+".service")
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		logWarn("service not installed")
		return nil
	}

	exec.Command("systemctl", "stop", serviceName).Run()
	exec.Command("systemctl", "disable", serviceName).Run()
	os.Remove(unitPath)
	exec.Command("systemctl", "daemon-reload").Run()

	logSuccess("service removed")
	return nil
}

func serviceStatus() error {
	out, err := exec.Command("systemctl", "is-active", serviceName).Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		logError("status: %s", status)
		return nil
	}
	logSuccess("status: %s", status)
	return nil
}
