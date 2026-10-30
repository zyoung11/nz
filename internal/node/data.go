package node

import (
	"fmt"
	"net/netip"
	"time"

	"nz/internal/logx"
	"nz/internal/protocol"
)

func (n *Node) serve() error {
	logx.Success("node started, server %v", n.serverAddr)
	n.lastServerSeen = time.Now()
	go n.readFromTUN()
	go n.keepAliveLoop()
	go n.serverWatch()
	select {}
}

func deliver(ch chan protocol.Envelope, env protocol.Envelope) {
	select {
	case ch <- env:
	default:
	}
}

func (n *Node) readFromUDP() {
	buf := make([]byte, 65536)
	for {
		nread, _, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		env, err := protocol.Unmarshal(buf[:nread])
		if err != nil {
			continue
		}
		n.lastServerSeen = time.Now()
		switch env.Type {
		case protocol.TypeData, protocol.TypeRelayData:
			n.handleData(env)
		case protocol.TypePeerQueryRpy:
			deliver(n.queryReplyCh, env)
		case protocol.TypeMachineListRpy:
			deliver(n.listReplyCh, env)
		case protocol.TypeJoinChallenge, protocol.TypeJoinReply:
			deliver(n.joinReplyCh, env)
		case protocol.TypeApproveNotify:
			deliver(n.approveCh, env)
		case protocol.TypeKeepAlive:
			signed, sig, err := protocol.ParseSigned(env.Body)
			if err == nil && !n.verifyServer(signed, sig) {
				n.serverAuthFailed()
			}
		}
	}
}

func (n *Node) readFromTUN() {
	packet := make([]byte, 65536)
	for {
		nread, err := n.tun.Read(packet)
		if err != nil {
			return
		}
		if nread < 20 {
			continue
		}
		raw := packet[:nread]
		if raw[0]>>4 != 4 {
			continue
		}
		dst := netip.AddrFrom4([4]byte{raw[16], raw[17], raw[18], raw[19]})
		if dst == n.vpnIP {
			continue
		}
		n.sendTo(dst, raw)
	}
}

func (n *Node) sendTo(dst netip.Addr, raw []byte) {
	p := n.ensurePeer(dst)
	if p == nil {
		return
	}
	if !p.canCall && !isReplyPacket(raw) {
		logx.Warn("new call to %v blocked by policy", dst)
		n.dropPeer(dst)
		return
	}
	cipher, err := protocol.Seal(p.key, raw)
	if err != nil {
		logx.Warn("%v", err)
		return
	}
	msg := protocol.SignedMessage{Type: protocol.TypeData, SrcIP: n.vpnIP, DstIP: dst, Payload: cipher}
	body := protocol.MarshalSigned(msg, n.id.Sign)
	env := protocol.Envelope{Type: protocol.TypeData, Body: body}
	addr := n.serverAddr
	if p.realAddr.IsValid() {
		addr = p.realAddr
	} else {
		env.Type = protocol.TypeRelayData
	}
	n.conn.WriteToUDPAddrPort(protocol.Marshal(env), addr)
}

const peerRefreshAfter = time.Second

func (n *Node) ensurePeer(dst netip.Addr) *peer {
	if dst == serverAddrOf(n) {
		return n.ensureServerPeer()
	}
	n.mu.Lock()
	p := n.peers[dst]
	n.mu.Unlock()
	if p != nil && time.Since(p.lastSeen) < peerRefreshAfter {
		return p
	}
	answer, err := n.queryPeer(dst)
	if err != nil {
		logx.Warn("peer query for %v failed: %v", dst, err)
		return nil
	}
	if !answer.Found {
		logx.Warn("unknown target %v", dst)
		return nil
	}
	key, err := n.id.SharedKey(answer.ExchPub)
	if err != nil {
		logx.Warn("%v", err)
		return nil
	}
	if p == nil {
		p = &peer{}
	}
	p.signPub = answer.SignPub
	p.exchPub = answer.ExchPub
	p.key = key
	p.canCall = answer.CanCall
	p.canCallBack = answer.CanCallBack
	p.lastSeen = time.Now()
	p.realAddr = netip.AddrPort{}
	if answer.HasAddr {
		p.realAddr = answer.Addr
	}
	n.mu.Lock()
	n.peers[dst] = p
	n.mu.Unlock()
	return p
}

func (n *Node) ensureServerPeer() *peer {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p, ok := n.peers[serverAddrOf(n)]; ok {
		return p
	}
	key, err := n.id.SharedKey(n.auth.ServerExchPub)
	if err != nil {
		logx.Warn("server key exchange failed: %v", err)
		return nil
	}
	p := &peer{
		signPub:     n.auth.ServerSignPub,
		exchPub:     n.auth.ServerExchPub,
		key:         key,
		realAddr:    n.serverAddr,
		canCall:     true,
		canCallBack: true,
		lastSeen:    time.Now(),
	}
	n.peers[serverAddrOf(n)] = p
	return p
}

// QueryMachines asks the server for the visible machine table.
func QueryMachines() ([]protocol.PeerInfo, error) {
	n := activeNode.Load()
	if n == nil {
		return nil, fmt.Errorf("node is not running")
	}
	msg := protocol.SignedMessage{
		Type:  protocol.TypeMachineList,
		SrcIP: n.vpnIP,
		DstIP: serverAddrOf(n),
	}
	body := protocol.MarshalSigned(msg, n.id.Sign)
	n.conn.WriteToUDPAddrPort(
		protocol.Marshal(protocol.Envelope{Type: protocol.TypeMachineList, Body: body}),
		n.serverAddr)
	env, err := waitEnvelope(n.listReplyCh, peerQueryTimeout)
	if err != nil {
		return nil, err
	}
	signed, sig, err := protocol.ParseSigned(env.Body)
	if err != nil {
		return nil, err
	}
	if !n.verifyServer(signed, sig) {
		n.serverAuthFailed()
		return nil, fmt.Errorf("bad server signature")
	}
	var list protocol.MachineListRpy
	if err := protocol.DecodeJSON(signed.Payload, &list); err != nil {
		return nil, err
	}
	return list.Peers, nil
}

func (n *Node) queryPeer(dst netip.Addr) (protocol.PeerAnswer, error) {
	msg := protocol.SignedMessage{
		Type:    protocol.TypePeerQuery,
		SrcIP:   n.vpnIP,
		DstIP:   serverAddrOf(n),
		Payload: protocol.PeerQueryPayload(dst),
	}
	body := protocol.MarshalSigned(msg, n.id.Sign)
	n.conn.WriteToUDPAddrPort(
		protocol.Marshal(protocol.Envelope{Type: protocol.TypePeerQuery, Body: body}),
		n.serverAddr)

	env, err := waitEnvelope(n.queryReplyCh, peerQueryTimeout)
	if err != nil {
		return protocol.PeerAnswer{}, err
	}
	signed, sig, err := protocol.ParseSigned(env.Body)
	if err != nil {
		return protocol.PeerAnswer{}, err
	}
	if !n.verifyServer(signed, sig) {
		return protocol.PeerAnswer{}, fmt.Errorf("bad server signature")
	}
	return protocol.ParsePeerAnswer(signed.Payload)
}

func serverAddrOf(n *Node) netip.Addr {
	return n.subnet.Addr().Next()
}

func (n *Node) verifyServer(m protocol.SignedMessage, sig []byte) bool {
	return protocol.VerifySigned(m, sig, n.auth.ServerSignPub)
}

// serverAuthFailed marks the server link stale so the watch loop
// rejoins and picks up a rotated server key.
func (n *Node) serverAuthFailed() {
	logx.Warn("server signature invalid, rejoining")
	n.lastServerSeen = time.Time{}
}

func (n *Node) handleData(env protocol.Envelope) {
	signed, sig, err := protocol.ParseSigned(env.Body)
	if err != nil {
		return
	}
	p := n.lookupPeer(signed.SrcIP)
	if p == nil {
		go n.ensurePeer(signed.SrcIP)
		logx.Warn("data from unknown peer %v dropped, discovering", signed.SrcIP)
		return
	}
	if !protocol.VerifySigned(signed, sig, p.signPub) {
		logx.Warn("bad signature from %v dropped", signed.SrcIP)
		return
	}
	if signed.SrcIP == n.vpnIP || signed.DstIP != n.vpnIP {
		return
	}
	plain, err := protocol.Open(p.key, signed.Payload)
	if err != nil {
		logx.Warn("decrypt from %v failed: %v", signed.SrcIP, err)
		return
	}
	if !p.canCallBack && !isReplyPacket(plain) {
		logx.Warn("new call from %v blocked by policy", signed.SrcIP)
		go n.ensurePeer(signed.SrcIP)
		return
	}
	if _, err := n.tun.Write(plain); err != nil {
		logx.Warn("tun write failed: %v", err)
	}
}

func (n *Node) lookupPeer(ip netip.Addr) *peer {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peers[ip]
}

func (n *Node) dropPeer(ip netip.Addr) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.peers, ip)
}

func (n *Node) keepAliveLoop() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for range ticker.C {
		msg := protocol.SignedMessage{
			Type:  protocol.TypeKeepAlive,
			SrcIP: n.vpnIP,
			DstIP: serverAddrOf(n),
		}
		body := protocol.MarshalSigned(msg, n.id.Sign)
		n.conn.WriteToUDPAddrPort(
			protocol.Marshal(protocol.Envelope{Type: protocol.TypeKeepAlive, Body: body}),
			n.serverAddr)
	}
}

func (n *Node) serverWatch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if time.Since(n.lastServerSeen) < serverStaleAfter {
			continue
		}
		logx.Warn("server lost, rejoining")
		n.lastServerSeen = time.Now()
		if err := n.join(); err != nil {
			logx.Warn("rejoin failed: %v", err)
		}
	}
}
