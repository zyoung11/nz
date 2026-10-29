package tun

import "io"

// Device is a TUN network device carrying raw IP packets.
type Device interface {
	io.ReadWriteCloser
	Name() string
}
