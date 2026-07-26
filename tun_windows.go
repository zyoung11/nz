//go:build windows

package main

import (
	"fmt"
	"net/netip"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"
)

type wgTunAdapter struct {
	dev  tun.Device
	name string
}

func (a *wgTunAdapter) Read(p []byte) (int, error) {
	bufs := [][]byte{p}
	sizes := []int{0}
	n, err := a.dev.Read(bufs, sizes, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return sizes[0], nil
}

func (a *wgTunAdapter) Write(p []byte) (int, error) {
	bufs := [][]byte{p}
	return a.dev.Write(bufs, 0)
}

func (a *wgTunAdapter) Close() error {
	return a.dev.Close()
}

func (a *wgTunAdapter) Name() string {
	return a.name
}

func createTUN(cfgName string, vpnIP netip.Addr, _ int) (tunDevice, error) {
	if err := initWintun(); err != nil {
		return nil, fmt.Errorf("wintun init failed: %w", err)
	}

	dev, err := tun.CreateTUN(cfgName, 9000)
	if err != nil {
		return nil, fmt.Errorf("创建TUN设备失败: %w（需要wintun.dll放在程序同目录或C:\\Windows\\System32）", err)
	}

	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to get TUN name: %w", err)
	}

	adapter := &wgTunAdapter{dev: dev, name: name}

	maskStr := "255.255.255.0"
	ipStr := vpnIP.String()

	setIPCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		fmt.Sprintf("name=\"%s\"", name),
		"static", ipStr, maskStr)
	if out, err := setIPCmd.CombinedOutput(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to set IP (admin required): %w (%s)", err, out)
	}

	addRouteCmd := exec.Command("netsh", "interface", "ip", "add", "route",
		"192.168.100.0/24", name)
	if out, err := addRouteCmd.CombinedOutput(); err != nil {
		logWarn("failed to add route (may already exist): %v (%s)", err, out)
	}

	return adapter, nil
}
