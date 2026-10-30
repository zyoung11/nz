package server

import (
	"net/netip"
	"time"

	"nz/internal/identity"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/state"
)

func (s *Server) handleSigned(env protocol.Envelope, remote netip.AddrPort) {
	signed, sig, err := protocol.ParseSigned(env.Body)
	if err != nil {
		return
	}
	src, ok := s.verifyMachine(signed, sig)
	if !ok {
		return
	}
	if isDataPlane(env.Type) && src.Status != state.MachineActive {
		return
	}
	s.rememberIPEndpoint(signed.SrcIP, remote)
	switch env.Type {
	case protocol.TypeRelayData:
		s.relayData(signed, env.Body, src)
	case protocol.TypeData:
		s.localData(signed, src)
	case protocol.TypePeerQuery:
		s.handlePeerQuery(signed, src, remote)
	case protocol.TypeMachineList:
		s.handleMachineList(signed, src)
	case protocol.TypeAdminRequest:
		s.handleAdminRequest(signed, src)
	case protocol.TypeKeepAlive:
		s.st.Touch(src.User, src.Name)
		s.sendSigned(protocol.SignedMessage{
			Type:  protocol.TypeKeepAlive,
			SrcIP: s.serverIP(),
			DstIP: signed.SrcIP,
		}, remote)
	}
}

// verifyMachine authenticates a signed message by the machine registered
// for the claimed source address, so a forged source address fails.
func (s *Server) verifyMachine(m protocol.SignedMessage, sig []byte) (state.Machine, bool) {
	machine, ok := s.st.FindMachineByIP(m.SrcIP.String())
	if !ok {
		logx.Warn("message from unregistered %v dropped", m.SrcIP)
		return state.Machine{}, false
	}
	signPub, _ := machineKeys(machine)
	if !protocol.VerifySigned(m, sig, signPub) {
		logx.Warn("bad signature from %s/%s dropped", machine.User, machine.Name)
		return state.Machine{}, false
	}
	return machine, true
}

func isDataPlane(typ protocol.MsgType) bool {
	return typ == protocol.TypeRelayData || typ == protocol.TypeData
}

const flowTTL = 60 * time.Second

// noteFlow records an allowed direction so reply packets pass back.
func (s *Server) noteFlow(src, dst netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[src.String()+"->"+dst.String()] = time.Now()
}

// hasRecentFlow reports whether dst sent traffic to src recently,
// which makes the current packet a reply instead of a new call.
func (s *Server) hasRecentFlow(src, dst netip.Addr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dst.String() + "->" + src.String()
	at, ok := s.flows[key]
	if !ok {
		return false
	}
	if time.Since(at) > flowTTL {
		delete(s.flows, key)
		return false
	}
	return true
}

func (s *Server) relayData(m protocol.SignedMessage, raw []byte, src state.Machine) {
	dst, ok := s.st.FindMachineByIP(m.DstIP.String())
	if !ok {
		return
	}
	if s.AllowRelay(src, dst) {
		s.noteFlow(m.SrcIP, m.DstIP)
	} else if !s.hasRecentFlow(m.SrcIP, m.DstIP) {
		logx.Warn("relay %s/%s -> %s/%s denied by policy", src.User, src.Name, dst.User, dst.Name)
		return
	}
	addr := s.ipEndpoint(m.DstIP)
	if !addr.IsValid() {
		logx.Warn("relay target %s/%s has no endpoint, dropped", dst.User, dst.Name)
		return
	}
	s.sendRaw(protocol.Envelope{Type: protocol.TypeRelayData, Body: raw}, addr)
}

func (s *Server) localData(m protocol.SignedMessage, src state.Machine) {
	if !s.AllowRelay(src, s.ServerMachine()) {
		logx.Warn("data %s/%s -> server denied by policy", src.User, src.Name)
		return
	}
	if s.tun == nil {
		return
	}
	_, exchPub := machineKeys(src)
	key, err := s.id.SharedKey(exchPub)
	if err != nil {
		return
	}
	plain, err := protocol.Open(key, m.Payload)
	if err != nil {
		logx.Warn("decrypt from %s/%s failed: %v", src.User, src.Name, err)
		return
	}
	s.tun.Write(plain)
}

func (s *Server) handlePeerQuery(m protocol.SignedMessage, src state.Machine, remote netip.AddrPort) {
	target, err := protocol.ParsePeerQuery(m.Payload)
	if err != nil {
		return
	}
	answer := protocol.PeerAnswer{Found: false}
	dst, found := s.st.FindMachineByIP(target.String())
	if found && dst.Status == state.MachineActive {
		answer = protocol.PeerAnswer{
			Found:       true,
			CanCall:     s.AllowRelay(src, dst),
			CanCallBack: s.AllowRelay(dst, src),
		}
		answer.SignPub, answer.ExchPub = machineKeys(dst)
		if s.AllowAddressExchange(src, dst) {
			if addr := s.ipEndpoint(target); addr.IsValid() {
				answer.HasAddr = true
				answer.Addr = addr
			}
		}
	}
	signed := protocol.SignedMessage{
		Type:    protocol.TypePeerQueryRpy,
		SrcIP:   s.serverIP(),
		DstIP:   m.SrcIP,
		Payload: protocol.MarshalPeerAnswer(answer),
	}
	s.sendSigned(signed, s.ipEndpoint(m.SrcIP))
	if answer.HasAddr {
		s.sendPeerIntro(src, dst, remote)
	}
}

// sendPeerIntro tells the target machine which endpoint is calling so
// both sides punch their NAT mappings at the same time.
func (s *Server) sendPeerIntro(src, dst state.Machine, remote netip.AddrPort) {
	intro := protocol.PeerIntro{VPNIP: src.VPNIP, Addr: remote}
	signed := protocol.SignedMessage{
		Type:    protocol.TypePeerIntro,
		SrcIP:   s.serverIP(),
		DstIP:   netip.MustParseAddr(dst.VPNIP),
		Payload: protocol.EncodeJSON(intro),
	}
	s.sendSigned(signed, s.ipEndpoint(netip.MustParseAddr(dst.VPNIP)))
}

func (s *Server) handleMachineList(m protocol.SignedMessage, src state.Machine) {
	var list protocol.MachineListRpy
	list.Peers = append(list.Peers, protocol.PeerInfo{
		User:    s.cfg.Admin.Name,
		Machine: "server",
		VPNIP:   s.serverIP().String(),
		Status:  "online",
		Trusted: true,
	})
	for _, other := range s.st.ActiveMachines() {
		if !s.AllowRelay(src, other) {
			continue
		}
		signPub, exchPub := machineKeys(other)
		status := "unknown"
		if !other.LastSeen.IsZero() {
			status = "offline"
			if time.Since(other.LastSeen) < 15*time.Second {
				status = "online"
			}
		}
		list.Peers = append(list.Peers, protocol.PeerInfo{
			User:        other.User,
			Machine:     other.Name,
			VPNIP:       other.VPNIP,
			SignPub:     signPub,
			ExchPub:     exchPub,
			Trusted:     other.Trusted || other.User == s.cfg.Admin.Name,
			Status:      status,
			Self:        other.VPNIP == m.SrcIP.String(),
			CanCall:     s.AllowRelay(src, other),
			CanCallBack: s.AllowRelay(other, src),
		})
	}
	signed := protocol.SignedMessage{
		Type:    protocol.TypeMachineListRpy,
		SrcIP:   s.serverIP(),
		DstIP:   m.SrcIP,
		Payload: protocol.EncodeJSON(list),
	}
	s.sendSigned(signed, s.ipEndpoint(m.SrcIP))
}

// readFromTUN forwards packets leaving the server TUN to the target
// machine, so replies to node traffic can flow back.
func (s *Server) readFromTUN() {
	packet := make([]byte, 65536)
	for {
		nread, err := s.tun.Read(packet)
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
		if dst == s.serverIP() {
			continue
		}
		s.sendLocalData(dst, raw)
	}
}

func (s *Server) sendLocalData(dst netip.Addr, raw []byte) {
	m, ok := s.st.FindMachineByIP(dst.String())
	if !ok {
		return
	}
	if !s.AllowRelay(s.ServerMachine(), m) {
		logx.Warn("data server -> %s/%s denied by policy", m.User, m.Name)
		return
	}
	addr := s.ipEndpoint(dst)
	if !addr.IsValid() {
		return
	}
	_, exchPub := machineKeys(m)
	key, err := s.id.SharedKey(exchPub)
	if err != nil {
		return
	}
	cipher, err := protocol.Seal(key, raw)
	if err != nil {
		return
	}
	msg := protocol.SignedMessage{Type: protocol.TypeData, SrcIP: s.serverIP(), DstIP: dst, Payload: cipher}
	body := protocol.MarshalSigned(msg, s.id.Sign)
	s.sendRaw(protocol.Envelope{Type: protocol.TypeData, Body: body}, addr)
}

func (s *Server) handleAdminRequest(m protocol.SignedMessage, src state.Machine) {
	var req protocol.AdminRequest
	if err := protocol.DecodeJSON(m.Payload, &req); err != nil {
		return
	}
	reply := protocol.AdminReply{OK: true}
	if src.User != s.adminName() {
		reply.OK = false
		reply.Error = "only admin machines can manage"
	} else if req.Command == "users" {
		data, err := s.UsersData(src.User, src.Name)
		reply.OK = err == nil
		reply.Data = data
		if err != nil {
			reply.Error = err.Error()
		}
	} else if err := s.AdminCommand(req.Command, req.Target); err != nil {
		reply.OK = false
		reply.Error = err.Error()
	} else {
		logx.Success("%s %s applied remotely", req.Command, req.Target)
	}
	signed := protocol.SignedMessage{
		Type:    protocol.TypeAdminRpy,
		SrcIP:   s.serverIP(),
		DstIP:   m.SrcIP,
		Payload: protocol.EncodeJSON(reply),
	}
	s.sendSigned(signed, s.ipEndpoint(m.SrcIP))
}

func (s *Server) sendSigned(m protocol.SignedMessage, addr netip.AddrPort) {
	if !addr.IsValid() {
		return
	}
	body := protocol.MarshalSigned(m, s.id.Sign)
	s.sendRaw(protocol.Envelope{Type: m.Type, Body: body}, addr)
}

func (s *Server) serverIP() netip.Addr {
	prefix, err := netip.ParsePrefix(s.cfg.Subnet)
	if err != nil {
		return netip.Addr{}
	}
	return prefix.Addr().Next()
}

func (s *Server) rememberIPEndpoint(ip netip.Addr, addr netip.AddrPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ipEndpoints[ip.Unmap()] = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
}

func (s *Server) ipEndpoint(ip netip.Addr) netip.AddrPort {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ipEndpoints[ip]
}

func machineKeys(m state.Machine) ([]byte, []byte) {
	blob, err := decodeB64(m.Public)
	if err != nil {
		return nil, nil
	}
	signPub, exchPub, err := identity.SplitPublic(blob)
	if err != nil {
		return nil, nil
	}
	return signPub, exchPub
}
