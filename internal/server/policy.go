package server

import (
	"encoding/base64"
	"fmt"

	"nz/internal/state"
)

// AllowRelay reports whether src may send traffic to dst over relay.
func (s *Server) AllowRelay(src, dst state.Machine) bool {
	if src.User == s.adminName() {
		return true
	}
	if src.User == dst.User {
		return true
	}
	if dst.User == s.adminName() {
		return src.Trusted
	}
	return false
}

// AllowP2P reports whether the pair may connect directly.
// A one-way allowance forces relay so the weaker side never learns addresses.
func (s *Server) AllowP2P(a, b state.Machine) bool {
	return s.AllowRelay(a, b) && s.AllowRelay(b, a)
}

// AllowAddressExchange reports whether real addresses may be shared for the pair.
func (s *Server) AllowAddressExchange(a, b state.Machine) bool {
	return s.AllowP2P(a, b)
}

// ServerMachine returns the pseudo machine entry for the server itself.
func (s *Server) ServerMachine() state.Machine {
	return state.Machine{User: s.adminName(), Name: "server", Status: state.MachineActive}
}

func (s *Server) adminName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Admin.Name
}

func decodeB64(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty value")
	}
	return base64.StdEncoding.DecodeString(s)
}
