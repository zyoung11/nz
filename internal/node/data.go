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
	n.readFromUDP()
	return nil
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
		env.Type = protocol.TypeData
		addr = p.realAddr
	} else {
		env.Type = protocol.TypeRelayData
	}
	n.conn.WriteToUDPAddrPort(protocol.Marshal(env), addr)
}

func (n *Node) ensurePeer(dst netip.Addr) *peer {
	n.mu.Lock()
	p := n.peers[dst]
	n.mu.Unlock()
	if p != nil {
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
	signPub, exchPub := answer.SignPub, answer.ExchPub
	key, err := n.id.SharedKey(exchPub)
	if err != nil {
		logx.Warn("%v", err)
		return nil
	}
	p = &peer{signPub: signPub, exchPub: exchPub, key: key, lastSeen: time.Now()}
	if answer.HasAddr {
		p.realAddr = answer.Addr
	}
	n.mu.Lock()
	n.peers[dst] = p
	n.mu.Unlock()
	return p
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

	select {
	case env := <-n.queryReplyCh:
		signed, sig, err := protocol.ParseSigned(env.Body)
		if err != nil {
			return protocol.PeerAnswer{}, err
		}
		if !n.verifyServer(signed, sig) {
			return protocol.PeerAnswer{}, fmt.Errorf("bad server signature")
		}
		return protocol.ParsePeerAnswer(signed.Payload)
	case <-time.After(peerQueryTimeout):
		return protocol.PeerAnswer{}, fmt.Errorf("peer query timeout")
	}
}

func serverAddrOf(n *Node) netip.Addr {
	return n.subnet.Addr().Next()
}

func (n *Node) verifyServer(m protocol.SignedMessage, sig []byte) bool {
	return protocol.VerifySigned(m, sig, n.auth.ServerSignPub)
}

func (n *Node) readFromUDP() {
	buf := make([]byte, 65536)
	for {
		nread, from, err := n.conn.ReadFromUDPAddrPort(buf)
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
			select {
			case n.queryReplyCh <- env:
			default:
			}
		case protocol.TypeKeepAlive:
		case protocol.TypeApproveNotify:
			n.handleApproveNotify(env)
		}
		_ = from
	}
}

func (n *Node) handleData(env protocol.Envelope) {
	signed, sig, err := protocol.ParseSigned(env.Body)
	if err != nil {
		return
	}
	p := n.lookupPeer(signed.SrcIP)
	if p == nil {
		logx.Warn("data from unknown peer %v dropped", signed.SrcIP)
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
	if _, err := n.tun.Write(plain); err != nil {
		logx.Warn("tun write failed: %v", err)
	}
	p.lastSeen = time.Now()
}

func (n *Node) lookupPeer(ip netip.Addr) *peer {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.peers[ip]
}

func (n *Node) handleApproveNotify(env protocol.Envelope) {
	var notify protocol.ApproveNotify
	if err := protocol.DecodeJSON(env.Body, &notify); err != nil {
		return
	}
	if n.vpnIP.IsValid() {
		return
	}
	if err := n.adoptVPNIP(notify.VPNIP); err != nil {
		logx.Warn("%v", err)
	}
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
