package server

import (
	"net/netip"

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
	s.rememberIPEndpoint(signed.SrcIP, remote)
	switch signed.Type {
	case protocol.TypeRelayData:
		s.relayData(signed, env.Body, src)
	case protocol.TypeData:
		s.localData(signed, src)
	case protocol.TypePeerQuery:
		s.handlePeerQuery(signed, src)
	case protocol.TypeMachineList:
		s.handleMachineList(signed, src)
	case protocol.TypeKeepAlive:
		s.st.Touch(src.User, src.Name)
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
	if machine.Status != state.MachineActive {
		return state.Machine{}, false
	}
	return machine, true
}

func (s *Server) relayData(m protocol.SignedMessage, raw []byte, src state.Machine) {
	dst, ok := s.st.FindMachineByIP(m.DstIP.String())
	if !ok {
		return
	}
	if !s.AllowRelay(src, dst) {
		logx.Warn("relay %s/%s -> %s/%s denied by policy", src.User, src.Name, dst.User, dst.Name)
		return
	}
	addr := s.ipEndpoint(m.DstIP)
	if !addr.IsValid() {
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

func (s *Server) handlePeerQuery(m protocol.SignedMessage, src state.Machine) {
	target, err := protocol.ParsePeerQuery(m.Payload)
	if err != nil {
		return
	}
	answer := protocol.PeerAnswer{Found: false}
	if dst, ok := s.st.FindMachineByIP(target.String()); ok && dst.Status == state.MachineActive {
		answer = protocol.PeerAnswer{Found: true}
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
}

func (s *Server) handleMachineList(m protocol.SignedMessage, src state.Machine) {
	var list protocol.MachineListRpy
	for _, other := range s.st.ActiveMachines() {
		if !s.AllowRelay(src, other) {
			continue
		}
		signPub, exchPub := machineKeys(other)
		list.Peers = append(list.Peers, protocol.PeerInfo{
			User:    other.User,
			Machine: other.Name,
			VPNIP:   other.VPNIP,
			SignPub: signPub,
			ExchPub: exchPub,
			Trusted: other.Trusted,
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
