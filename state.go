package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
)

const stateFileName = "netzero-state.json"

func stateFilePath() string {
	exe, err := os.Executable()
	if err != nil {
		return stateFileName
	}
	return filepath.Join(filepath.Dir(exe), stateFileName)
}

type nodeRecord struct {
	Name     string `json:"name"`
	VPNIP    string `json:"vpn_ip"`
	RealAddr string `json:"real_addr"`
}

type serverState struct {
	mu       sync.Mutex
	NextIP   int          `json:"next_ip"`
	Password string       `json:"password"`
	Nodes    []nodeRecord `json:"nodes"`
}

func loadServerState(password string) (*serverState, error) {
	s := &serverState{
		NextIP:   2,
		Password: password,
		Nodes:    make([]nodeRecord, 0),
	}
	data, err := os.ReadFile(stateFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return s, s.save()
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Nodes == nil {
		s.Nodes = make([]nodeRecord, 0)
	}
	return s, nil
}

func (s *serverState) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFilePath(), data, 0600)
}

func (s *serverState) allocateIP(name string, desiredIP int, realAddr netip.AddrPort) (netip.Addr, error) {
	realAddr = normalizeAddr(realAddr)
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range s.Nodes {
		if n.Name == name {
			return netip.ParseAddr(n.VPNIP)
		}
	}

	if desiredIP >= 2 && desiredIP <= 254 {
		desired := netip.AddrFrom4([4]byte{192, 168, 100, byte(desiredIP)})
		for _, n := range s.Nodes {
			if n.VPNIP == desired.String() {
				return netip.Addr{}, fmt.Errorf("IP %v already in use by %v", desired, n.Name)
			}
		}
		ip := desired
		s.Nodes = append(s.Nodes, nodeRecord{Name: name, VPNIP: ip.String(), RealAddr: realAddr.String()})
		if err := s.save(); err != nil {
			return netip.Addr{}, err
		}
		return ip, nil
	}

	for {
		ip := netip.AddrFrom4([4]byte{192, 168, 100, byte(s.NextIP)})
		s.NextIP++
		free := true
		for _, n := range s.Nodes {
			if n.VPNIP == ip.String() {
				free = false
				break
			}
		}
		if free {
			s.Nodes = append(s.Nodes, nodeRecord{Name: name, VPNIP: ip.String(), RealAddr: realAddr.String()})
			if err := s.save(); err != nil {
				return netip.Addr{}, err
			}
			return ip, nil
		}
	}
}

func (s *serverState) updateRealAddr(vpnIP netip.Addr, realAddr netip.AddrPort) {
	realAddr = normalizeAddr(realAddr)
	s.mu.Lock()
	defer s.mu.Unlock()

	ipStr := vpnIP.String()
	for i, n := range s.Nodes {
		if n.VPNIP == ipStr {
			s.Nodes[i].RealAddr = realAddr.String()
			_ = s.save()
			return
		}
	}
}

func (s *serverState) findByName(name string) (netip.Addr, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range s.Nodes {
		if n.Name == name {
			ip, err := netip.ParseAddr(n.VPNIP)
			if err != nil {
				return netip.Addr{}, false
			}
			return ip, true
		}
	}
	return netip.Addr{}, false
}

func (s *serverState) findByVPNIP(vpnIP netip.Addr) (netip.AddrPort, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ipStr := vpnIP.String()
	for _, n := range s.Nodes {
		if n.VPNIP == ipStr {
			addr, err := netip.ParseAddrPort(n.RealAddr)
			if err != nil {
				return netip.AddrPort{}, false
			}
			return addr, true
		}
	}
	return netip.AddrPort{}, false
}

func (s *serverState) findByRealAddr(realAddr netip.AddrPort) (netip.Addr, bool) {
	realAddr = normalizeAddr(realAddr)
	s.mu.Lock()
	defer s.mu.Unlock()

	raStr := realAddr.String()
	for _, n := range s.Nodes {
		if n.RealAddr == raStr {
			ip, err := netip.ParseAddr(n.VPNIP)
			if err != nil {
				return netip.Addr{}, false
			}
			return ip, true
		}
	}
	return netip.Addr{}, false
}
