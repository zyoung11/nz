package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const serverVPNIP = "192.168.100.1"
const vpnNetworkAddr = "192.168.100.0"
const vpnPrefix = 24

const relayActivityWindow = 5 * time.Minute

type server struct {
	conn          *net.UDPConn
	state         *serverState
	key           []byte
	peerKeys      sync.Map
	vpnIP         netip.Addr
	tun           tunDevice
	peers         *peerMap
	name          string
	relayActivity map[string]time.Time
}

func runServer(password string, port int, tunName, name, configPath string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	addr := net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)))
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen UDP: %w", err)
	}
	defer conn.Close()

	state, err := loadServerState(password, configPath)
	if err != nil {
		return fmt.Errorf("failed to load state file: %w", err)
	}

	key := sharedKey(password)
	vpnIP := netip.MustParseAddr(serverVPNIP)

	tun, err := createTUN(tunName, vpnIP, 16)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer tun.Close()

	s := &server{
		conn:          conn,
		state:         state,
		key:           key,
		vpnIP:         vpnIP,
		tun:           tun,
		peers:         newPeerMap(),
		name:          name,
		relayActivity: make(map[string]time.Time),
	}
	if s.name == "" {
		s.name = "server"
	}

	logSuccess("%s started, listening on port %d", s.name, port)
	logInfo("VPN IP: %v/%d", vpnIP, vpnPrefix)
	logInfo("TUN: %s", tun.Name())

	go s.readFromTUN()
	go s.heartbeatCheck(ctx)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 65536)
	for {
		n, remote, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				logInfo("received signal, shutting down...")
				return nil
			default:
			}
			if isClosedError(err) {
				return nil
			}
			continue
		}

		msg, err := unmarshalMessage(buf[:n])
		if err != nil {
			continue
		}

		s.handleMessage(msg, remote)
	}
}

func (s *server) handleMessage(msg message, remote netip.AddrPort) {
	remote = normalizeAddr(remote)
	switch msg.Type {
	case msgHello:
		s.handleHello(msg.Payload, remote)
	case msgConfirm:
		s.handleConfirm(msg.Payload, remote)
	case msgPeerQuery:
		s.handlePeerQuery(msg.Payload, remote)
	case msgPeerHello:
		s.handlePeerHello(msg.Payload, remote)
	case msgPeerHelloRpy:
		s.handlePeerHelloReply(msg.Payload, remote)
	case msgPeerList:
		s.handlePeerList(msg.Payload, remote)
	case msgDisconnect:
		s.handleDisconnect(remote)
	case msgData:
		s.handleData(msg.Payload, remote)
	case msgRelayData:
		s.handleRelayData(msg.Payload, remote)
	case msgKeepAlive:
		s.handleKeepAlive(msg.Payload, remote)
	case msgError:
	}
}

func (s *server) handleHello(payload []byte, remote netip.AddrPort) {
	nodeName, desiredIP, clientNonce, err := unmarshalHello(payload)
	if err != nil {
		return
	}

	encNonce, err := encrypt(s.key, clientNonce)
	if err != nil {
		return
	}

	serverNonce, err := genNonce()
	if err != nil {
		return
	}

	s.peerKeys.Store(remote.String(), &pendingClient{
		name:        nodeName,
		clientNonce: clientNonce,
		serverNonce: serverNonce,
		desiredIP:   desiredIP,
	})

	replyPayload := marshalHelloReply(encNonce, serverNonce)
	reply := marshalMessage(message{Type: msgHelloReply, Payload: replyPayload})
	s.sendTo(reply, remote)

	logInfo("received handshake: %v (%v)", nodeName, remote)
}

func (s *server) handleConfirm(payload []byte, remote netip.AddrPort) {
	val, ok := s.peerKeys.LoadAndDelete(remote.String())
	if !ok {
		return
	}
	pc := val.(*pendingClient)

	decrypted, err := decrypt(s.key, payload)
	if err != nil {
		logError("auth failed: %v, from %v", err, remote)
		return
	}

	if string(decrypted) != string(pc.serverNonce) {
		logError("auth failed: nonce mismatch, from %v", remote)
		return
	}

	existingIP, hasExisting := s.state.findByName(pc.name)
	var vpnIP netip.Addr
	if hasExisting {
		existingAddr, found := s.state.findByVPNIP(existingIP)
		if found && existingAddr != remote {
			existingPeer := s.peers.get(existingIP)
			if existingPeer != nil && existingPeer.state == peerConnected {
				logWarn("%v reconnecting, replacing old connection from %v", pc.name, existingAddr)
			}
		}
		vpnIP = existingIP
		logInfo("node reconnected: %v (VPN IP: %v)", pc.name, vpnIP)
	} else {
		vpnIP, err = s.state.allocateIP(pc.name, pc.desiredIP, remote)
		if err != nil {
			logError("failed to allocate IP: %v", err)
			return
		}
		logInfo("new node joined: %v → %v", pc.name, vpnIP)
	}

	s.state.updateRealAddr(vpnIP, remote)
	s.peers.setConnected(vpnIP, remote)
	logInfo("peer added: %v (%v)", vpnIP, remote)

	replyPayload := marshalConfirmReply(vpnIP, vpnPrefix)
	encPayload, err := encrypt(s.key, replyPayload)
	if err != nil {
		return
	}

	reply := marshalMessage(message{Type: msgConfirmReply, Payload: encPayload})
	s.sendTo(reply, remote)

	logSuccess("node authenticated: %v (VPN IP: %v)", pc.name, vpnIP)
}

func (s *server) handlePeerQuery(payload []byte, remote netip.AddrPort) {
	decPayload, err := decrypt(s.key, payload)
	if err != nil {
		return
	}

	targetIP, err := unmarshalPeerQuery(decPayload)
	if err != nil {
		return
	}

	srcIP, hasSrc := s.state.findByRealAddr(remote)
	if !hasSrc {
		return
	}

	if targetIP == s.vpnIP {
		localAddr := s.conn.LocalAddr().(*net.UDPAddr).AddrPort()

		replyPayload := marshalPeerQueryRpy(s.vpnIP, localAddr)
		encPayload, _ := encrypt(s.key, replyPayload)
		reply := marshalMessage(message{Type: msgPeerQueryRpy, Payload: encPayload})
		s.sendTo(reply, remote)
		return
	}

	targetAddr, found := s.state.findByVPNIP(targetIP)
	if !found {
		errPayload := marshalPeerQueryRpy(targetIP, netip.AddrPort{})
		encErrPayload, _ := encrypt(s.key, errPayload)
		errMsg := marshalMessage(message{Type: msgPeerQueryRpy, Payload: encErrPayload})
		s.sendTo(errMsg, remote)
		return
	}

	replyPayload := marshalPeerQueryRpy(targetIP, targetAddr)
	encPayload, err := encrypt(s.key, replyPayload)
	if err != nil {
		return
	}

	reply := marshalMessage(message{Type: msgPeerQueryRpy, Payload: encPayload})
	s.sendTo(reply, remote)

	introPayload := marshalPeerIntro(srcIP, remote)
	encIntroPayload, err := encrypt(s.key, introPayload)
	if err != nil {
		return
	}

	introMsg := marshalMessage(message{Type: msgPeerIntro, Payload: encIntroPayload})
	s.sendTo(introMsg, targetAddr)

	logInfo("peer discovery: %v ↔ %v", remote, targetAddr)
}

func (s *server) handlePeerHelloReply(payload []byte, remote netip.AddrPort) {
	if len(payload) < 4 {
		return
	}
	var srcIP4 [4]byte
	copy(srcIP4[:], payload[0:4])
	srcIP := netip.AddrFrom4(srcIP4)

	p := s.peers.get(srcIP)
	if p != nil && p.state == peerProbing {
		s.peers.setConnected(srcIP, remote)
		if addr, found := s.state.findByVPNIP(srcIP); found && addr != remote {
			s.state.updateRealAddr(srcIP, remote)
			logWarn("node address drifted, corrected: %v -> %v", srcIP, remote)
		}
		logSuccess("%v probe replied, connection restored", srcIP)
	}
}

func (s *server) handlePeerHello(payload []byte, remote netip.AddrPort) {
	if len(payload) < 4 {
		return
	}
	var srcIP4 [4]byte
	copy(srcIP4[:], payload[0:4])
	srcIP := netip.AddrFrom4(srcIP4)

	encNonce := payload[4:]
	nonce, err := decrypt(s.key, encNonce)
	if err != nil {
		return
	}

	s.peers.setConnected(srcIP, remote)

	encReply, err := encrypt(s.key, nonce)
	if err != nil {
		return
	}

	replyPayload := make([]byte, 4+len(encReply))
	copy(replyPayload[0:4], s.vpnIP.AsSlice())
	copy(replyPayload[4:], encReply)

	replyMsg := marshalMessage(message{Type: msgPeerHelloRpy, Payload: replyPayload})
	s.sendTo(replyMsg, remote)
}

func (s *server) handleData(payload []byte, remote netip.AddrPort) {
	decData, err := decrypt(s.key, payload)
	if err != nil {
		return
	}

	srcIP, data, err := unmarshalData(decData)
	if err != nil {
		return
	}

	if srcIP == s.vpnIP {
		return
	}

	peer := s.peers.get(srcIP)
	if peer == nil {
		logError("data from unknown peer: %v (%v)", srcIP, remote)
		return
	}

	if peer.realAddr != remote || peer.state != peerConnected {
		s.peers.setConnected(srcIP, remote)
		if addr, found := s.state.findByVPNIP(srcIP); found && addr != remote {
			s.state.updateRealAddr(srcIP, remote)
			logWarn("node address drifted, corrected: %v -> %v", srcIP, remote)
		}
	} else {
		peer.lastSeen = time.Now()
	}

	s.tun.Write(data)
}

func (s *server) handleRelayData(payload []byte, remote netip.AddrPort) {
	decPayload, err := decrypt(s.key, payload)
	if err != nil {
		return
	}

	srcIP, targetIP, data, err := unmarshalRelayData(decPayload)
	if err != nil {
		return
	}

	if addr, found := s.state.findByVPNIP(srcIP); found && addr != remote {
		s.state.updateRealAddr(srcIP, remote)
		s.peers.setConnected(srcIP, remote)
		logWarn("node address drifted, corrected: %v -> %v", srcIP, remote)
	}

	if targetIP == s.vpnIP {
		s.tun.Write(data)
		return
	}

	targetAddr, found := s.state.findByVPNIP(targetIP)
	if !found {
		return
	}

	targetPeer := s.peers.get(targetIP)
	if targetPeer == nil || targetPeer.state != peerConnected {
		return
	}

	relayPayload := marshalRelayData(srcIP, targetIP, data)
	encPayload, err := encrypt(s.key, relayPayload)
	if err != nil {
		return
	}

	msg := marshalMessage(message{Type: msgRelayData, Payload: encPayload})
	s.sendTo(msg, targetAddr)
	s.relayActivity[pairKey(srcIP, targetIP)] = time.Now()
}

// readFromTUN reads outgoing IP packets from the TUN device and forwards them.
func (s *server) readFromTUN() {
	packet := make([]byte, 65536)
	for {
		nread, err := s.tun.Read(packet)
		if err != nil {
			if err == io.EOF {
				return
			}
			continue
		}

		if nread < 20 {
			continue
		}

		s.routeOutboundPacket(packet[:nread])
	}
}

func (s *server) routeOutboundPacket(raw []byte) {
	if raw[0]>>4 != 4 {
		return
	}

	dstIP := netip.AddrFrom4([4]byte{raw[16], raw[17], raw[18], raw[19]})

	if dstIP == s.vpnIP {
		return
	}

	if !isVPNIP(dstIP) {
		return
	}

	if dstIP.IsLinkLocalUnicast() || dstIP.IsLinkLocalMulticast() || dstIP.IsMulticast() {
		return
	}

	peer := s.peers.get(dstIP)
	if peer != nil && peer.realAddr.IsValid() && peer.isP2PActive() {
		encData, err := encrypt(s.key, marshalData(s.vpnIP, raw))
		if err != nil {
			return
		}
		dataMsg := marshalMessage(message{Type: msgData, Payload: encData})
		addr := net.UDPAddrFromAddrPort(peer.realAddr)
		_, err = s.conn.WriteToUDP(dataMsg, addr)
		if err != nil {
			logError("UDP send failed: %v", err)
		}
	} else if peer != nil && peer.realAddr.IsValid() && peer.state == peerConnected {
		s.sendPeerHello(peer.vpnIP, peer.realAddr)
	}
}

func (s *server) handleKeepAlive(payload []byte, remote netip.AddrPort) {
	ip, err := unmarshalKeepAlive(payload)
	if err != nil {
		return
	}

	p := s.peers.get(ip)
	if p == nil {
		if _, found := s.state.findByVPNIP(ip); !found {
			return
		}
		logInfo("%v (%v) re-registered via keepalive after restart", ip, remote)
	}

	if p != nil && p.realAddr != remote {
		logInfo("%v address changed: %v -> %v", ip, p.realAddr, remote)
		s.state.updateRealAddr(ip, remote)
	} else if p != nil && p.state != peerConnected {
		logInfo("%v (%v) recovered via keepalive", ip, remote)
	}

	s.peers.setConnected(ip, remote)
	if p := s.peers.get(ip); p != nil {
		p.sendKey = s.key
		p.recvKey = s.key
	}

	ack := marshalMessage(message{Type: msgKeepAlive, Payload: s.vpnIP.AsSlice()})
	s.sendTo(ack, remote)
}

func (s *server) heartbeatCheck(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			for _, p := range s.peers.all() {
				switch p.state {
				case peerConnected:
					if !p.isP2PActive() {
						p.state = peerProbing
						p.probeSent = now
						logWarn("%v (%v) heartbeat lost, probing", p.vpnIP, p.realAddr)
						s.sendPeerHello(p.vpnIP, p.realAddr)
						go s.reprobePeer(p.vpnIP, p.realAddr)
					}
				case peerProbing:
					if now.Sub(p.probeSent) > 15*time.Second {
						p.state = peerDisconnected
						logWarn("%v (%v) timed out", p.vpnIP, p.realAddr)
					}
				}
			}
		}
	}
}

func (s *server) reprobePeer(vpnIP netip.Addr, target netip.AddrPort) {
	time.Sleep(3 * time.Second)
	s.sendPeerHello(vpnIP, target)
	time.Sleep(3 * time.Second)
	s.sendPeerHello(vpnIP, target)
}

func (s *server) sendPeerHello(_ netip.Addr, target netip.AddrPort) {
	nonce, err := genNonce()
	if err != nil {
		return
	}
	encNonce, err := encrypt(s.key, nonce)
	if err != nil {
		return
	}
	helloPayload := make([]byte, 4+len(encNonce))
	copy(helloPayload[0:4], s.vpnIP.AsSlice())
	copy(helloPayload[4:], encNonce)
	helloMsg := marshalMessage(message{Type: msgPeerHello, Payload: helloPayload})
	addr := net.UDPAddrFromAddrPort(target)
	s.conn.WriteToUDP(helloMsg, addr)
}

func (s *server) handleDisconnect(remote netip.AddrPort) {
	peer := s.peers.getByReal(remote)
	if peer != nil {
		peer.state = peerDisconnected
		peer.realAddr = netip.AddrPort{}
		logInfo("%v (%v) disconnected", peer.vpnIP, remote)
	}
}

func (s *server) handlePeerList(payload []byte, remote netip.AddrPort) {
	_, err := decrypt(s.key, payload)
	if err != nil {
		return
	}

	type peerEntry struct {
		Name   string `json:"name"`
		IP     string `json:"ip"`
		Status string `json:"status"`
	}

	var list []peerEntry

	list = append(list, peerEntry{Name: s.name, IP: s.vpnIP.String(), Status: "online"})

	now := time.Now()
	s.state.mu.Lock()
	for _, n := range s.state.Nodes {
		ip, _ := netip.ParseAddr(n.VPNIP)
		status := "offline"
		if p := s.peers.get(ip); p != nil {
			switch p.state {
			case peerConnected:
				if now.Sub(p.lastSeen) < 15*time.Second {
					status = "online"
				} else {
					status = "offline"
					s.peers.remove(ip)
				}
			case peerProbing:
				status = "probing"
			case peerPunching:
				status = "connecting"
			}
		}
		list = append(list, peerEntry{Name: n.Name, IP: n.VPNIP, Status: status})
	}
	s.state.mu.Unlock()

	data, _ := json.Marshal(list)
	encPayload, err := encrypt(s.key, data)
	if err != nil {
		return
	}
	reply := marshalMessage(message{Type: msgPeerListRpy, Payload: encPayload})
	s.sendTo(reply, remote)
}

func (s *server) sendTo(msg []byte, addr netip.AddrPort) {
	udpAddr := net.UDPAddrFromAddrPort(addr)
	_, err := s.conn.WriteToUDP(msg, udpAddr)
	if err != nil {
		logError("send failed: %v", err)
	}
}

func pairKey(a, b netip.Addr) string {
	if a.Less(b) {
		return a.String() + "|" + b.String()
	}
	return b.String() + "|" + a.String()
}

type pendingClient struct {
	name        string
	clientNonce []byte
	serverNonce []byte
	desiredIP   int
}
