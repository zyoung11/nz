//go:build linux

package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"unsafe"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type nativeTun struct {
	fd   int
	name string
}

func (t *nativeTun) Read(p []byte) (int, error) {
	for {
		n, err := unix.Read(t.fd, p)
		if err != nil {
			if err == unix.EAGAIN {
				continue
			}
			if err == unix.EBADF {
				return 0, os.ErrClosed
			}
			return 0, err
		}
		return n, nil
	}
}

func (t *nativeTun) Write(p []byte) (int, error) {
	for {
		n, err := unix.Write(t.fd, p)
		if err != nil {
			if err == unix.EAGAIN {
				continue
			}
			if err == unix.EBADF {
				return 0, os.ErrClosed
			}
			return 0, err
		}
		return n, nil
	}
}

func (t *nativeTun) Close() error {
	return unix.Close(t.fd)
}

func (t *nativeTun) Name() string {
	return t.name
}

type ifReq struct {
	Name  [unix.IFNAMSIZ]byte
	Flags uint16
	pad   [8]byte
}

func ioctl(fd uintptr, req uintptr, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

func createTUN(cfgName string, vpnIP netip.Addr, _ int) (tunDevice, error) {
	cleanupTUN(cfgName)

	fd, err := unix.Open("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			os.MkdirAll("/dev/net", 0755)
			unix.Mknod("/dev/net/tun", unix.S_IFCHR|0600, int(unix.Mkdev(10, 200)))
			fd, err = unix.Open("/dev/net/tun", os.O_RDWR, 0)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to open /dev/net/tun: %w", err)
		}
	}

	var req ifReq
	req.Flags = unix.IFF_TUN | unix.IFF_NO_PI
	copy(req.Name[:], cfgName)

	if err := ioctl(uintptr(fd), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req))); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF failed: %w", err)
	}

	name := strings.TrimRight(string(req.Name[:]), "\x00")
	tun := &nativeTun{fd: fd, name: name}

	if err := unix.SetNonblock(fd, false); err != nil {
		tun.Close()
		return nil, fmt.Errorf("set nonblock failed: %w", err)
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to get TUN link: %w", err)
	}

	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   vpnIP.AsSlice(),
			Mask: net.CIDRMask(vpnPrefix, 32),
		},
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to set IP address: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to bring TUN up: %w", err)
	}

	cidr := netip.PrefixFrom(netip.MustParseAddr(vpnNetworkAddr), vpnPrefix)
	dr := &net.IPNet{
		IP:   cidr.Masked().Addr().AsSlice(),
		Mask: net.CIDRMask(cidr.Bits(), cidr.Addr().BitLen()),
	}

	nr := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dr,
		Scope:     unix.RT_SCOPE_LINK,
		Table:     unix.RT_TABLE_MAIN,
		Type:      unix.RTN_UNICAST,
	}
	if err := netlink.RouteReplace(&nr); err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to add route: %w", err)
	}

	return tun, nil
}

func cleanupTUN(name string) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return
	}
	netlink.LinkDel(link)
}
