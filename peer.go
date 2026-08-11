package main

import (
	"net/netip"
	"sync"
	"time"
)

type peerState int

const (
	peerDisconnected peerState = iota
	peerPunching
	peerConnected
	peerProbing
)

type peer struct {
	vpnIP      netip.Addr
	realAddr   netip.AddrPort
	state      peerState
	lastSeen   time.Time
	sendKey    []byte
	recvKey    []byte
	punchStart time.Time
	punchCount int
	probeSent  time.Time
}

type peerMap struct {
	mu       sync.RWMutex
	peers    map[netip.Addr]*peer
	byRealIP map[netip.AddrPort]*peer
}

func newPeerMap() *peerMap {
	return &peerMap{
		peers:    make(map[netip.Addr]*peer),
		byRealIP: make(map[netip.AddrPort]*peer),
	}
}

func (pm *peerMap) get(vpnIP netip.Addr) *peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.peers[vpnIP]
}

func (pm *peerMap) getByReal(addr netip.AddrPort) *peer {
	addr = normalizeAddr(addr)
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.byRealIP[addr]
}

func (pm *peerMap) getOrAdd(vpnIP netip.Addr) *peer {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if existing, ok := pm.peers[vpnIP]; ok {
		return existing
	}

	p := &peer{
		vpnIP:    vpnIP,
		state:    peerDisconnected,
		lastSeen: time.Now(),
	}
	pm.peers[vpnIP] = p
	return p
}

func (pm *peerMap) updateAddr(vpnIP netip.Addr, realAddr netip.AddrPort, state peerState) {
	realAddr = normalizeAddr(realAddr)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.peers[vpnIP]
	if !ok {
		p = &peer{vpnIP: vpnIP}
		pm.peers[vpnIP] = p
	}
	if p.realAddr.IsValid() {
		delete(pm.byRealIP, p.realAddr)
	}
	p.realAddr = realAddr
	p.state = state
	p.lastSeen = time.Now()
	pm.byRealIP[realAddr] = p
}

func (pm *peerMap) setPunching(vpnIP netip.Addr, realAddr netip.AddrPort) {
	realAddr = normalizeAddr(realAddr)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.peers[vpnIP]
	if !ok {
		p = &peer{vpnIP: vpnIP}
		pm.peers[vpnIP] = p
	}
	if p.realAddr.IsValid() {
		delete(pm.byRealIP, p.realAddr)
	}
	p.realAddr = realAddr
	p.state = peerPunching
	p.punchStart = time.Now()
	p.punchCount = 0
	pm.byRealIP[realAddr] = p
}

func (pm *peerMap) setConnected(vpnIP netip.Addr, realAddr netip.AddrPort) {
	realAddr = normalizeAddr(realAddr)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.peers[vpnIP]
	if !ok {
		p = &peer{vpnIP: vpnIP}
		pm.peers[vpnIP] = p
	}
	if p.realAddr.IsValid() {
		delete(pm.byRealIP, p.realAddr)
	}
	p.realAddr = realAddr
	p.state = peerConnected
	p.lastSeen = time.Now()
	p.punchCount = 0
	pm.byRealIP[realAddr] = p
}

func (pm *peerMap) remove(vpnIP netip.Addr) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.peers[vpnIP]; ok {
		delete(pm.byRealIP, p.realAddr)
		delete(pm.peers, vpnIP)
	}
}

func (pm *peerMap) all() []*peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]*peer, 0, len(pm.peers))
	for _, p := range pm.peers {
		result = append(result, p)
	}
	return result
}
