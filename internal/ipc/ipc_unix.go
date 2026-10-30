//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Listen opens the admin Unix socket at path.
func Listen(path string) (net.Listener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", path, err)
	}
	os.Chmod(path, 0666)
	return ln, nil
}

// connUID returns the uid of the peer connected via unix socket.
func connUID(conn net.Conn) uint32 {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	var uid uint32
	raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil {
			uid = cred.Uid
		}
	})
	return uid
}

// Dial connects to the admin Unix socket at path.
func Dial(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	return conn, nil
}
