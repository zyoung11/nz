//go:build windows

package ipc

import (
	"errors"
	"net"
)

var errNotImplemented = errors.New("named pipe admin socket is not implemented yet")

// Listen opens the admin named pipe.
func Listen(_ string) (net.Listener, error) {
	return nil, errNotImplemented
}

// Dial connects to the admin named pipe.
func Dial(_ string) (net.Conn, error) {
	return nil, errNotImplemented
}

// socketPath returns the admin named pipe location.
func socketPath() string {
	return `\\.\pipe\n0`
}

// connUID reports the caller, named pipes enforce access in the ACL.
func connUID(_ net.Conn) uint32 {
	return 0
}
