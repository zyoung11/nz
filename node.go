package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/signal"
	"syscall"
	"time"
)

const (
	keepAliveInterval   = 20 * time.Second
	punchInterval       = 200 * time.Millisecond
	maxPunchAttempts    = 10
	punchTimeout        = 5 * time.Second
	handshakeTimeout    = 10 * time.Second
	retryDelay          = 2 * time.Second
)

type node struct {
	conn       *net.UDPConn
	serverAddr netip.AddrPort
	name       string
	desiredIP  int
	routeMode  string
	key        []byte
	vpnIP      netip.Addr
	netmask    int
	tun        tunDevice
	peers      *peerMap
	connected  bool
}

func runNode(serverAddr netip.AddrPort, name, password, tunName, routeMode string, desiredIP int) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	n := &node{
		serverAddr: serverAddr,
		name:       name,
		desiredIP:  desiredIP,
		routeMode:  routeMode,
		peers:      newPeerMap(),
		key:        sharedKey(password),
	}

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
	logInfo("server: %v", serverAddr)

	go n.readFromTUN()
	go n.readFromUDP()
	go n.keepAliveLoop()

	defer func() {
		disconnectMsg := marshalMessage(message{Type: msgDisconnect, Payload: nil})
		n.conn.Write(disconnectMsg)
	}()

	<-ctx.Done()
	logInfo("received signal, shutting down...")
	return nil
}

func (n *node) connectToServer(ctx context.Context) error {
	addr := net.UDPAddrFromAddrPort(n.serverAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("failed to connect UDP: %w", err)
	}
	n.conn = conn

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
		return nil
	}
}

func (n *node) doHandshake() error {
	clientNonce, err := genNonce()
	if err != nil {
		return err
	}

	helloMsg := marshalMessage(message{Type: msgHello, Payload: marshalHello(n.name, n.desiredIP, clientNonce)})
	if _, err := n.conn.Write(helloMsg); err != nil {
		return fmt.Errorf("failed to send Hello: %w", err)
	}

	buf := make([]byte, 65536)
	n.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	nread, err := n.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("等待HelloReply超时: %w", err)
	}
	n.conn.SetReadDeadline(time.Time{})

	reply, err := unmarshalMessage(buf[:nread])
	if err != nil || reply.Type != msgHelloReply {
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
	if _, err := n.conn.Write(confirmMsg); err != nil {
		return fmt.Errorf("failed to send Confirm: %w", err)
	}

	n.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	nread, err = n.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("等待ConfirmReply超时: %w", err)
	}
	n.conn.SetReadDeadline(time.Time{})

	confirmReply, err := unmarshalMessage(buf[:nread])
	if err != nil || confirmReply.Type != msgConfirmReply {
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
	n.connected = true

	serverVPN := netip.MustParseAddr(serverVPNIP)
	n.peers.setConnected(serverVPN, n.serverAddr)
	if sp := n.peers.get(serverVPN); sp != nil {
		sp.sendKey = n.key
		sp.recvKey = n.key
	}
	logInfo("server peer added: %v (%v)", serverVPN, n.serverAddr)

	return nil
}

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

	if peer != nil && peer.state == peerConnected {
		encData, err := encrypt(peer.sendKey, raw)
		if err != nil {
			return
		}
		dataMsg := marshalMessage(message{Type: msgData, Payload: encData})
		_, err = n.conn.Write(dataMsg)
		if err != nil {
			logError("UDP send failed: %v", err)
		}
		return
	}

	if peer != nil && peer.realAddr.IsValid() {
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
	relayPayload := marshalRelayData(targetIP, data)
	encPayload, err := encrypt(n.key, relayPayload)
	if err != nil {
		return
	}

	msg := marshalMessage(message{Type: msgRelayData, Payload: encPayload})
	n.conn.Write(msg)
}

func (n *node) requestPeerInfo(targetIP netip.Addr) {
	queryPayload := marshalPeerQuery(targetIP)
	encPayload, err := encrypt(n.key, queryPayload)
	if err != nil {
		return
	}

	msg := marshalMessage(message{Type: msgPeerQuery, Payload: encPayload})
	n.conn.Write(msg)
}

func (n *node) readFromUDP() {
	buf := make([]byte, 65536)
	for {
		nread, remote, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if isClosedError(err) {
				return
			}
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
	case msgKeepAlive, msgError, msgDisconnect, msgPong:
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

func (n *node) handlePing(remote netip.AddrPort) {
	pongPayload := n.vpnIP.AsSlice()
	pongMsg := marshalMessage(message{Type: msgPong, Payload: pongPayload})
	n.conn.Write(pongMsg)
}

func (n *node) handleData(payload []byte, remote netip.AddrPort) {
	peer := n.peers.getByReal(remote)
	if peer == nil {
		logError("received data from unknown peer: %v", remote)
		return
	}

	decData, err := decrypt(peer.recvKey, payload)
	if err != nil {
		logError("decrypt failed: %v", err)
		return
	}

	_, err = n.tun.Write(decData)
	if err != nil {
		logError("TUN write failed: %v", err)
		return
	}
	peer.lastSeen = time.Now()
}

func (n *node) handleRelayData(payload []byte, _ netip.AddrPort) {
	decPayload, err := decrypt(n.key, payload)
	if err != nil {
		return
	}

	srcIP, data, err := unmarshalRelayData(decPayload)
	if err != nil {
		return
	}

	n.tun.Write(data)

	p := n.peers.get(srcIP)
	if p != nil && p.state != peerConnected {
		p.lastSeen = time.Now()
	}
}

func (n *node) keepAliveLoop() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	for range ticker.C {
		kaMsg := marshalMessage(message{Type: msgKeepAlive, Payload: nil})
		n.conn.Write(kaMsg)
	}
}

func isVPNIP(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	b := ip.As4()
	return b[0] == 192 && b[1] == 168 && b[2] == 100
}
