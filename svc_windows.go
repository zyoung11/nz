//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const shutdownEventName = "nz-shutdown-event-v1"

func initWintun() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	dllPath := filepath.Join(filepath.Dir(exePath), "wintun.dll")
	if _, err := os.Stat(dllPath); err == nil {
		return nil
	}
	return os.WriteFile(dllPath, wintunDLL, 0644)
}

func installService() error {
	oldKey, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		registry.SET_VALUE)
	if err == nil {
		oldKey.DeleteValue("nz")
		oldKey.Close()
	}

	stopRunningInstance()

	if err := setAutoStart(true); err != nil {
		return err
	}

	cmd := exec.Command("schtasks", "/Run", "/TN", "nz")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks run failed: %v (output: %s)", err, string(out))
	}
	logSuccess("auto-start enabled and started (task scheduler)")
	return nil
}

func uninstallService() error {
	enabled, _ := isAutoStartEnabled()
	if enabled {
		if err := setAutoStart(false); err != nil {
			return err
		}
	}

	stopRunningInstance()

	logSuccess("auto-start disabled and stopped")
	return nil
}

func signalShutdown() bool {
	name, err := windows.UTF16PtrFromString(shutdownEventName)
	if err != nil {
		return false
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h) == nil
}

func stopRunningInstance() {
	for range 20 {
		if !signalShutdown() {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	killLeftoverInstances()
}

func killLeftoverInstances() {
	exec.Command("taskkill", "/IM", "nz.exe", "/F", "/FI", fmt.Sprintf("PID ne %d", os.Getpid())).Run()
	time.Sleep(500 * time.Millisecond)
}

func isServiceRunning(mode string) bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq nz.exe", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "nz.exe")
}
