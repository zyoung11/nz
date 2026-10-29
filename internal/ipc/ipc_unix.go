//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
)

// Listen opens the admin Unix socket at path.
func Listen(path string) (net.Listener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", path, err)
	}
	return ln, nil
}

// Dial connects to the admin Unix socket at path.
func Dial(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	return conn, nil
}
