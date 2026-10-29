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
