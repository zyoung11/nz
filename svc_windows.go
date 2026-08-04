//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

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

	enabled, _ := isAutoStartEnabled()
	if enabled {
		logWarn("auto-start already enabled")
		return nil
	}
	if err := setAutoStart(true); err != nil {
		return err
	}
	logSuccess("auto-start enabled (task scheduler)")
	return nil
}

func uninstallService() error {
	enabled, _ := isAutoStartEnabled()
	if !enabled {
		logWarn("auto-start not enabled")
		return nil
	}
	if err := setAutoStart(false); err != nil {
		return err
	}
	logSuccess("auto-start disabled")
	return nil
}
