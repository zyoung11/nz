//go:build linux

package tun

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

// Create opens a TUN device, sets the address and adds the subnet route.
func Create(name string, ip netip.Addr, subnet netip.Prefix) (Device, error) {
	Cleanup(name)

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
	copy(req.Name[:], name)

	if err := ioctl(uintptr(fd), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req))); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF failed: %w", err)
	}

	devName := strings.TrimRight(string(req.Name[:]), "\x00")
	t := &nativeTun{fd: fd, name: devName}

	if err := unix.SetNonblock(fd, false); err != nil {
		t.Close()
		return nil, fmt.Errorf("set nonblock failed: %w", err)
	}

	link, err := netlink.LinkByName(devName)
	if err != nil {
		t.Close()
		return nil, fmt.Errorf("failed to get TUN link: %w", err)
	}

	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip.AsSlice(),
			Mask: net.CIDRMask(subnet.Bits(), 32),
		},
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		t.Close()
		return nil, fmt.Errorf("failed to set IP address: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		t.Close()
		return nil, fmt.Errorf("failed to bring TUN up: %w", err)
	}

	dr := &net.IPNet{
		IP:   subnet.Masked().Addr().AsSlice(),
		Mask: net.CIDRMask(subnet.Bits(), 32),
	}
	nr := netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dr,
		Scope:     unix.RT_SCOPE_LINK,
		Table:     unix.RT_TABLE_MAIN,
		Type:      unix.RTN_UNICAST,
	}
	if err := netlink.RouteReplace(&nr); err != nil {
		t.Close()
		return nil, fmt.Errorf("failed to add route: %w", err)
	}

	return t, nil
}

// Cleanup deletes a leftover TUN device with the given name.
func Cleanup(name string) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return
	}
	netlink.LinkDel(link)
}

// CheckPermission reports whether this process may create TUN devices.
func CheckPermission() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("tun devices need root, run with sudo")
	}
	return nil
}
