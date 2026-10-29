//go:build windows

package tun

import (
	"fmt"
	"net"
	"net/netip"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"

	"nz/internal/logx"
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

// Create opens a wintun device, sets the address and adds the subnet route.
func Create(name string, ip netip.Addr, subnet netip.Prefix) (Device, error) {
	dev, err := tun.CreateTUN(name, 9000)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN device: %w (wintun.dll must be next to the binary)", err)
	}

	devName, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to get TUN name: %w", err)
	}

	adapter := &wgTunAdapter{dev: dev, name: devName}

	mask := net.IP(net.CIDRMask(subnet.Bits(), 32)).String()
	setIPCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		fmt.Sprintf("name=%q", devName),
		"static", ip.String(), mask)
	if out, err := setIPCmd.CombinedOutput(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to set IP (admin required): %w (%s)", err, out)
	}

	addRouteCmd := exec.Command("netsh", "interface", "ip", "add", "route",
		subnet.Masked().String(), devName)
	if out, err := addRouteCmd.CombinedOutput(); err != nil {
		logx.Warn("failed to add route: %v (%s)", err, out)
	}

	return adapter, nil
}

// Cleanup has nothing to do on Windows, wintun removal is automatic.
func Cleanup(_ string) {}
