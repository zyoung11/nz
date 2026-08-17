package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	keepAliveInterval      = 20 * time.Second
	punchInterval          = 200 * time.Millisecond
	maxPunchAttempts       = 10
	punchTimeout           = 5 * time.Second
	handshakeTimeout       = 10 * time.Second
	retryDelay             = 2 * time.Second
	serverStaleTimeout     = 90 * time.Second
	p2pStaleTimeout        = 90 * time.Second
	reconnectCheckInterval = 10 * time.Second
)

var serverVPNAddr = netip.MustParseAddr(serverVPNIP)

type node struct {
	conn           *net.UDPConn
	serverAddr     netip.AddrPort
	serverHost     string
	serverPort     int
	name           string
	desiredIP      int
	routeMode      string
	key            []byte
	vpnIP          netip.Addr
	netmask        int
	tun            tunDevice
	peers          *peerMap
	connected      atomic.Bool
	serverLastSeen atomic.Int64
	handshaking    atomic.Bool
	punchMu        sync.Mutex
	lastPunch      map[netip.Addr]time.Time
	reconnectMu    sync.Mutex
}

func runNode(serverHost string, port int, name, password, tunName, routeMode string, desiredIP int) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	n := &node{
		serverHost: serverHost,
		serverPort: port,
		name:       name,
		desiredIP:  desiredIP,
		routeMode:  routeMode,
		peers:      newPeerMap(),
		key:        sharedKey(password),
		lastPunch:  make(map[netip.Addr]time.Time),
	}
	n.serverLastSeen.Store(time.Now().UnixNano())

	if err := n.connectToServer(ctx); err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	var err error
	n.tun, err = createTUN(tunName, n.vpnIP, n.netmask)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer n.tun.Close()
	defer n.conn.Close()

	logSuccess("node started")
	logInfo("VPN IP: %v/%d", n.vpnIP, vpnPrefix)
	logInfo("TUN: %s", n.tun.Name())
	logInfo("server: %v", n.serverAddr)

	go n.readFromTUN()
	go n.readFromUDP()
	go n.keepAliveLoop()
	go n.peerKeepAliveLoop()
	go n.reconnectLoop(ctx)

	defer func() {
		disconnectMsg := marshalMessage(message{Type: msgDisconnect, Payload: nil})
		n.sendToServer(disconnectMsg)
	}()

	<-ctx.Done()
	logInfo("received signal, shutting down...")
	return nil
}

func (n *node) connectToServer(ctx context.Context) error {
	serverAddr, err := resolveAddr(n.serverHost, n.serverPort)
	if err != nil {
		return fmt.Errorf("failed to resolve server: %w", err)
	}
	n.serverAddr = serverAddr

	if n.conn == nil {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			return fmt.Errorf("failed to listen UDP: %w", err)
		}
		n.conn = conn
	}

	for attempt := 1; ; attempt++ {
		if err := n.doHandshake(); err != nil {
			logWarn("handshake failed, retrying in %v (attempt %d)", retryDelay*time.Duration(min(attempt, 10)), attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay * time.Duration(min(attempt, 10))):
			}
			continue
		}
		n.serverLastSeen.Store(time.Now().UnixNano())
		return nil
	}
}

func (n *node) doHandshake() error {
	n.handshaking.Store(true)
	defer n.handshaking.Store(false)

	clientNonce, err := genNonce()
	if err != nil {
		return err
	}

	helloMsg := marshalMessage(message{Type: msgHello, Payload: marshalHello(n.name, n.desiredIP, clientNonce)})
	if _, err := n.sendToServer(helloMsg); err != nil {
		return fmt.Errorf("failed to send Hello: %w", err)
	}

	reply, err := n.waitServerMessage(handshakeTimeout)
	if err != nil {
		return fmt.Errorf("HelloReply timeout: %w", err)
	}
	if reply.Type != msgHelloReply {
		return fmt.Errorf("invalid HelloReply")
	}

	encClientNonce, serverNonce, err := unmarshalHelloReply(reply.Payload)
	if err != nil {
		return err
	}

	decryptedNonce, err := decrypt(n.key, encClientNonce)
	if err != nil {
		return fmt.Errorf("wrong password")
	}
	if string(decryptedNonce) != string(clientNonce) {
		return fmt.Errorf("wrong password")
	}

	encServerNonce, err := encrypt(n.key, serverNonce)
	if err != nil {
		return err
	}

	confirmMsg := marshalMessage(message{Type: msgConfirm, Payload: encServerNonce})
	if _, err := n.sendToServer(confirmMsg); err != nil {
		return fmt.Errorf("failed to send Confirm: %w", err)
	}

	confirmReply, err := n.waitServerMessage(handshakeTimeout)
	if err != nil {
		return fmt.Errorf("ConfirmReply timeout: %w", err)
	}
	if confirmReply.Type != msgConfirmReply {
		return fmt.Errorf("invalid ConfirmReply")
	}

	decPayload, err := decrypt(n.key, confirmReply.Payload)
	if err != nil {
		return fmt.Errorf("failed to decrypt ConfirmReply: %w", err)
	}

	vpnIP, netmask, err := unmarshalConfirmReply(decPayload)
	if err != nil {
		return fmt.Errorf("failed to parse ConfirmReply: %w", err)
	}

	n.vpnIP = vpnIP
	n.netmask = netmask
	n.connected.Store(true)

	n.peers.setConnected(serverVPNAddr, n.serverAddr)
	if sp := n.peers.get(serverVPNAddr); sp != nil {
		sp.sendKey = n.key
		sp.recvKey = n.key
	}
	logInfo("server peer added: %v (%v)", serverVPNAddr, n.serverAddr)

	return nil
}

func (n *node) sendToServer(msg []byte) (int, error) {
	addr := net.UDPAddrFromAddrPort(n.serverAddr)
	return n.conn.WriteToUDP(msg, addr)
}

func (n *node) waitServerMessage(timeout time.Duration) (message, error) {
	buf := make([]byte, 65536)
	n.conn.SetReadDeadline(time.Now().Add(timeout))
	defer n.conn.SetReadDeadline(time.Time{})
	for {
		nread, from, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return message{}, err
		}
		if normalizeAddr(from) != n.serverAddr {
			continue
		}
		msg, err := unmarshalMessage(buf[:nread])
		if err != nil {
			continue
		}
		return msg, nil
	}
}

// readFromTUN reads outgoing IP packets from the TUN device and forwards them.
func (n *node) readFromTUN() {
	packet := make([]byte, 65536)
	for {
		nread, err := n.tun.Read(packet)
		if err != nil {
			if err == io.EOF {
				return
			}
			continue
		}

		if nread < 20 {
			continue
		}

		n.routeOutboundPacket(packet[:nread])
	}
}

func (n *node) routeOutboundPacket(raw []byte) {
	if raw[0]>>4 != 4 {
		return
	}

	dstIP := netip.AddrFrom4([4]byte{raw[16], raw[17], raw[18], raw[19]})

	if dstIP == n.vpnIP {
		return
	}

	if !isVPNIP(dstIP) {
		return
	}

	if dstIP.IsLinkLocalUnicast() || dstIP.IsLinkLocalMulticast() || dstIP.IsMulticast() {
		return
	}

	peer := n.peers.get(dstIP)

	if peer != nil && (dstIP == serverVPNAddr || peer.isP2PActive()) {
		encData, err := encrypt(peer.sendKey, marshalData(n.vpnIP, raw))
		if err != nil {
			return
		}
		dataMsg := marshalMessage(message{Type: msgData, Payload: encData})
		addr := net.UDPAddrFromAddrPort(peer.realAddr)
		_, err = n.conn.WriteToUDP(dataMsg, addr)
		if err != nil {
			logError("UDP send failed: %v", err)
		}
		return
	}

	if peer != nil && peer.realAddr.IsValid() {
		if peer.state == peerConnected {
			logWarn("%v P2P stale, falling back to relay and re-probing", dstIP)
			n.peers.setPunching(dstIP, peer.realAddr)
			n.requestPeerInfo(dstIP)
		}
		switch n.routeMode {
		case "p2p":
			go n.punchPeer(dstIP, peer.realAddr)
		case "relay":
			n.sendRelayPacket(dstIP, raw)
		default:
			n.sendRelayPacket(dstIP, raw)
			go n.punchPeer(dstIP, peer.realAddr)
		}
		return
	}

	n.requestPeerInfo(dstIP)

	if peer == nil {
		peer = n.peers.getOrAdd(dstIP)
	}
}

func (n *node) sendRelayPacket(targetIP netip.Addr, data []byte) {
	relayPayload := marshalRelayData(n.vpnIP, targetIP, data)
	encPayload, err := encrypt(n.key, relayPayload)
	if err != nil {
		return
	}

	msg := marshalMessage(message{Type: msgRelayData, Payload: encPayload})
	n.sendToServer(msg)
}

func (n *node) requestPeerInfo(targetIP netip.Addr) {
	queryPayload := marshalPeerQuery(targetIP)
	encPayload, err := encrypt(n.key, queryPayload)
	if err != nil {
		return
	}

	msg := marshalMessage(message{Type: msgPeerQuery, Payload: encPayload})
	n.sendToServer(msg)
}

func (n *node) readFromUDP() {
	buf := make([]byte, 65536)
	for {
		if n.handshaking.Load() {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		nread, remote, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if isClosedError(err) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		msg, err := unmarshalMessage(buf[:nread])
		if err != nil {
			continue
		}

		n.handleUDPMessage(msg, remote)
	}
}

func (n *node) handleUDPMessage(msg message, remote netip.AddrPort) {
	remote = normalizeAddr(remote)
	if remote == n.serverAddr {
		n.serverLastSeen.Store(time.Now().UnixNano())
	}
	switch msg.Type {
	case msgPeerQueryRpy:
		n.handlePeerQueryReply(msg.Payload, remote)
	case msgPeerIntro:
		n.handlePeerIntro(msg.Payload, remote)
	case msgPeerHello:
		n.handlePeerHello(msg.Payload, remote)
	case msgPeerHelloRpy:
		n.handlePeerHelloReply(msg.Payload, remote)
	case msgData:
		n.handleData(msg.Payload, remote)
	case msgPing:
		n.handlePing(remote)
	case msgRelayData:
		n.handleRelayData(msg.Payload, remote)
	case msgKeepAlive:
		n.handleKeepAlive(msg.Payload, remote)
	case msgError, msgDisconnect, msgPong:
	}
}

func (n *node) handlePeerQueryReply(payload []byte, _ netip.AddrPort) {
	decPayload, err := decrypt(n.key, payload)
	if err != nil {
		return
	}
	targetIP, targetAddr, err := unmarshalPeerIntro(decPayload)
	if err != nil {
		return
	}

	if !targetAddr.IsValid() {
		logWarn("target node offline: %v", targetIP)
		return
	}

	n.peers.setPunching(targetIP, targetAddr)
	if n.routeMode != "relay" {
		go n.punchPeer(targetIP, targetAddr)
	}

	logInfo("discovered node: %v (%v)", targetIP, targetAddr)
}

func (n *node) handlePeerIntro(payload []byte, _ netip.AddrPort) {
	decPayload, err := decrypt(n.key, payload)
	if err != nil {
		return
	}
	introIP, introAddr, err := unmarshalPeerIntro(decPayload)
	if err != nil {
		return
	}

	n.peers.setPunching(introIP, introAddr)
	if n.routeMode != "relay" {
		go n.punchPeer(introIP, introAddr)
	}

	logInfo("received peer intro: %v (%v)", introIP, introAddr)
}

func (n *node) punchPeer(targetIP netip.Addr, targetAddr netip.AddrPort) {
	n.punchMu.Lock()
	if t, ok := n.lastPunch[targetIP]; ok && time.Since(t) < punchTimeout {
		n.punchMu.Unlock()
		return
	}
	n.lastPunch[targetIP] = time.Now()
	n.punchMu.Unlock()

	for range maxPunchAttempts {
		p := n.peers.get(targetIP)
		if p != nil && p.state == peerConnected {
			return
		}

		nonce, err := genNonce()
		if err != nil {
			return
		}

		encNonce, err := encrypt(n.key, nonce)
		if err != nil {
			return
		}

		helloPayload := make([]byte, 4+len(encNonce))
		copy(helloPayload[0:4], n.vpnIP.AsSlice())
		copy(helloPayload[4:], encNonce)

		helloMsg := marshalMessage(message{Type: msgPeerHello, Payload: helloPayload})
		addr := net.UDPAddrFromAddrPort(targetAddr)
		n.conn.WriteToUDP(helloMsg, addr)

		time.Sleep(punchInterval)
	}
}

func (n *node) handlePeerHello(payload []byte, remote netip.AddrPort) {
	if len(payload) < 4 {
		return
	}
	var srcIP4 [4]byte
	copy(srcIP4[:], payload[0:4])
	srcIP := netip.AddrFrom4(srcIP4)

	encNonce := payload[4:]
	nonce, err := decrypt(n.key, encNonce)
	if err != nil {
		return
	}

	n.peers.setConnected(srcIP, remote)

	p := n.peers.get(srcIP)
	if p != nil {
		p.sendKey = n.key
		p.recvKey = n.key
	}

	encReply, err := encrypt(n.key, nonce)
	if err != nil {
		return
	}

	replyPayload := make([]byte, 4+len(encReply))
	copy(replyPayload[0:4], n.vpnIP.AsSlice())
	copy(replyPayload[4:], encReply)

	replyMsg := marshalMessage(message{Type: msgPeerHelloRpy, Payload: replyPayload})
	addr := net.UDPAddrFromAddrPort(remote)
	n.conn.WriteToUDP(replyMsg, addr)

	logSuccess("P2P established: %v ↔ %v", n.vpnIP, srcIP)
}

func (n *node) handlePeerHelloReply(payload []byte, remote netip.AddrPort) {
	if len(payload) < 4 {
		return
	}
	var srcIP4 [4]byte
	copy(srcIP4[:], payload[0:4])
	srcIP := netip.AddrFrom4(srcIP4)

	encNonce := payload[4:]
	_, err := decrypt(n.key, encNonce)
	if err != nil {
		return
	}

	n.peers.setConnected(srcIP, remote)

	p := n.peers.get(srcIP)
	if p != nil {
		p.sendKey = n.key
		p.recvKey = n.key
	}

	logSuccess("P2P established: %v ↔ %v", n.vpnIP, srcIP)
}

func (n *node) handleKeepAlive(payload []byte, remote netip.AddrPort) {
	if remote == n.serverAddr {
		if p := n.peers.get(serverVPNAddr); p != nil {
			p.lastSeen = time.Now()
		}
		return
	}

	if ip, err := unmarshalKeepAlive(payload); err == nil {
		if p := n.peers.get(ip); p != nil {
			if p.realAddr != remote {
				n.peers.setConnected(ip, remote)
				if p := n.peers.get(ip); p != nil {
					p.sendKey = n.key
					p.recvKey = n.key
				}
				logWarn("peer address drifted, corrected: %v -> %v", ip, remote)
			} else {
				p.lastSeen = time.Now()
			}
			return
		}
	}

	if p := n.peers.getByReal(remote); p != nil {
		p.lastSeen = time.Now()
	}
}

func (n *node) handlePing(remote netip.AddrPort) {
	peers := n.peers.all()
	payload := make([]byte, 5+5*len(peers))
	copy(payload[0:4], n.vpnIP.AsSlice())
	idx := 4
	count := 0
	for _, p := range peers {
		if p.vpnIP == serverVPNAddr {
			continue
		}
		mode := routeRelay
		if p.isP2PActive() {
			mode = routeP2P
		}
		copy(payload[idx:idx+4], p.vpnIP.AsSlice())
		payload[idx+4] = mode
		idx += 5
		count++
	}
	payload[4] = byte(count)
	payload = payload[:5+5*count]

	pongMsg := marshalMessage(message{Type: msgPong, Payload: payload})
	addr := net.UDPAddrFromAddrPort(remote)
	n.conn.WriteToUDP(pongMsg, addr)
}

func (n *node) handleData(payload []byte, remote netip.AddrPort) {
	key := n.key
	if peer := n.peers.getByReal(remote); peer != nil {
		key = peer.recvKey
	}

	decData, err := decrypt(key, payload)
	if err != nil {
		logError("decrypt failed: %v", err)
		return
	}

	srcIP, data, err := unmarshalData(decData)
	if err != nil {
		return
	}

	if srcIP == n.vpnIP {
		return
	}

	peer := n.peers.get(srcIP)
	if peer == nil {
		logError("data from unknown peer: %v (%v)", srcIP, remote)
		return
	}

	if peer.realAddr != remote || peer.state != peerConnected {
		n.peers.setConnected(srcIP, remote)
		if p := n.peers.get(srcIP); p != nil {
			p.sendKey = n.key
			p.recvKey = n.key
		}
		logWarn("peer address drifted, corrected: %v -> %v", srcIP, remote)
	} else {
		peer.updateP2PSeen()
	}

	_, err = n.tun.Write(data)
	if err != nil {
		logError("TUN write failed: %v", err)
	}
}

func (n *node) handleRelayData(payload []byte, _ netip.AddrPort) {
	decPayload, err := decrypt(n.key, payload)
	if err != nil {
		return
	}

	srcIP, _, data, err := unmarshalRelayData(decPayload)
	if err != nil {
		return
	}

	n.tun.Write(data)

	p := n.peers.get(srcIP)
	if p != nil && p.state != peerConnected {
		p.updateRelaySeen()
	}
}

func (n *node) keepAliveLoop() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for range ticker.C {
		if !n.connected.Load() {
			continue
		}
		kaMsg := marshalMessage(message{Type: msgKeepAlive, Payload: n.vpnIP.AsSlice()})
		n.sendToServer(kaMsg)
	}
}

func (n *node) peerKeepAliveLoop() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for range ticker.C {
		if !n.connected.Load() {
			continue
		}
		now := time.Now()
		for _, p := range n.peers.all() {
			if p.vpnIP == serverVPNAddr {
				continue
			}
			switch p.state {
			case peerConnected:
				kaMsg := marshalMessage(message{Type: msgKeepAlive, Payload: n.vpnIP.AsSlice()})
				addr := net.UDPAddrFromAddrPort(p.realAddr)
				n.conn.WriteToUDP(kaMsg, addr)

				if !p.isP2PActive() {
					logWarn("%v P2P stale (%v), falling back to relay and re-probing", p.vpnIP, p.realAddr)
					n.peers.setPunching(p.vpnIP, p.realAddr)
					n.requestPeerInfo(p.vpnIP)
				}
			case peerPunching:
				if now.Sub(p.punchStart) > punchTimeout {
					n.requestPeerInfo(p.vpnIP)
				}
			}
		}
	}
}

func (n *node) reconnectLoop(ctx context.Context) {
	ticker := time.NewTicker(reconnectCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !n.connected.Load() {
			continue
		}

		last := time.Unix(0, n.serverLastSeen.Load())
		if time.Since(last) < serverStaleTimeout {
			continue
		}

		if !n.reconnectMu.TryLock() {
			continue
		}

		logWarn("server connection lost (last seen %v ago), reconnecting...", time.Since(last).Round(time.Second))
		err := n.connectToServer(ctx)
		n.reconnectMu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logError("reconnect failed: %v", err)
			continue
		}
		logSuccess("reconnected to server")
	}
}

func isVPNIP(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	b := ip.As4()
	return b[0] == 192 && b[1] == 168 && b[2] == 100
}
