//go:build windows

package ipc

import (
	"net"

	"github.com/Microsoft/go-winio"
)

const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"

// Listen opens the admin named pipe.
func Listen(_ string) (net.Listener, error) {
	return winio.ListenPipe(socketPath(), &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
}

// Dial connects to the admin named pipe.
func Dial(_ string) (net.Conn, error) {
	return winio.DialPipe(socketPath(), nil)
}

// socketPath returns the admin named pipe location.
func socketPath() string {
	return `\\.\pipe\nz`
}

// connUID reports the caller, the pipe ACL already limits access to
// administrators so every caller passes the root check.
func connUID(_ net.Conn) uint32 {
	return 0
}
