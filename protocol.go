package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

const protocolVersion byte = 0x02

type msgType byte

const (
	msgHello        msgType = 0x01
	msgHelloReply   msgType = 0x02
	msgConfirm      msgType = 0x03
	msgConfirmReply msgType = 0x04
	msgPeerQuery    msgType = 0x05
	msgPeerQueryRpy msgType = 0x06
	msgPeerIntro    msgType = 0x07
	msgPeerHello    msgType = 0x10
	msgPeerHelloRpy msgType = 0x11
	msgPeerList     msgType = 0x12
	msgPeerListRpy  msgType = 0x13
	msgDisconnect   msgType = 0x14
	msgData         msgType = 0x20
	msgKeepAlive    msgType = 0x0E
	msgRelayData    msgType = 0x30
	msgError        msgType = 0xFF
)

type message struct {
	Type    msgType
	Payload []byte
}

func marshalMessage(m message) []byte {
	buf := make([]byte, 4+len(m.Payload))
	buf[0] = protocolVersion
	buf[1] = byte(m.Type)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(m.Payload)))
	copy(buf[4:], m.Payload)
	return buf
}

func unmarshalMessage(raw []byte) (message, error) {
	if len(raw) < 4 {
		return message{}, fmt.Errorf("message too short: %d bytes", len(raw))
	}
	if raw[0] != protocolVersion {
		return message{}, fmt.Errorf("unknown protocol version: %d", raw[0])
	}
	plen := binary.BigEndian.Uint16(raw[2:4])
	if len(raw) < int(4+plen) {
		return message{}, fmt.Errorf("message truncated: expected %d, got %d", 4+plen, len(raw))
	}
	return message{
		Type:    msgType(raw[1]),
		Payload: raw[4 : 4+plen],
	}, nil
}

func marshalHello(name string, desiredIP int, clientNonce []byte) []byte {
	buf := make([]byte, 2+len(name)+len(clientNonce))
	buf[0] = byte(len(name))
	buf[1] = byte(desiredIP)
	n := 2
	n += copy(buf[n:], name)
	copy(buf[n:], clientNonce)
	return buf
}

func unmarshalHello(payload []byte) (string, int, []byte, error) {
	if len(payload) < 2 {
		return "", 0, nil, fmt.Errorf("Hello too short")
	}
	nameLen := int(payload[0])
	desiredIP := int(payload[1])
	if len(payload) < 2+nameLen+nonceSize {
		return "", 0, nil, fmt.Errorf("Hello truncated")
	}
	name := string(payload[2 : 2+nameLen])
	nonce := payload[2+nameLen:]
	return name, desiredIP, nonce, nil
}

func marshalHelloReply(encClientNonce, serverNonce []byte) []byte {
	buf := make([]byte, 2+len(encClientNonce)+len(serverNonce))
	buf[0] = byte(len(encClientNonce))
	buf[1] = byte(len(serverNonce))
	n := 2
	n += copy(buf[n:], encClientNonce)
	copy(buf[n:], serverNonce)
	return buf
}

func unmarshalHelloReply(payload []byte) (encClientNonce, serverNonce []byte, err error) {
	if len(payload) < 2 {
		return nil, nil, fmt.Errorf("HelloReply too short")
	}
	encLen := int(payload[0])
	srvLen := int(payload[1])
	if len(payload) < 2+encLen+srvLen {
		return nil, nil, fmt.Errorf("HelloReply truncated")
	}
	encClientNonce = make([]byte, encLen)
	copy(encClientNonce, payload[2:2+encLen])
	serverNonce = make([]byte, srvLen)
	copy(serverNonce, payload[2+encLen:2+encLen+srvLen])
	return
}

func marshalConfirmReply(vpnIP netip.Addr, netmask int) []byte {
	buf := make([]byte, 5)
	ip4 := vpnIP.As4()
	copy(buf[0:4], ip4[:])
	buf[4] = byte(netmask)
	return buf
}

func unmarshalConfirmReply(payload []byte) (netip.Addr, int, error) {
	if len(payload) < 5 {
		return netip.Addr{}, 0, fmt.Errorf("ConfirmReply too short")
	}
	var ip4 [4]byte
	copy(ip4[:], payload[0:4])
	return netip.AddrFrom4(ip4), int(payload[4]), nil
}

func marshalPeerQuery(vpnIP netip.Addr) []byte {
	return vpnIP.AsSlice()
}

func unmarshalPeerQuery(payload []byte) (netip.Addr, error) {
	if len(payload) < 4 {
		return netip.Addr{}, fmt.Errorf("PeerQuery too short")
	}
	var ip4 [4]byte
	copy(ip4[:], payload[0:4])
	return netip.AddrFrom4(ip4), nil
}

func marshalPeerIntro(vpnIP netip.Addr, realAddr netip.AddrPort) []byte {
	buf := make([]byte, 10)
	if vpnIP.Is4() {
		ip4 := vpnIP.As4()
		copy(buf[0:4], ip4[:])
	}
	if realAddr.Addr().Is4() {
		ra := realAddr.Addr().As4()
		copy(buf[4:8], ra[:])
	}
	binary.BigEndian.PutUint16(buf[8:10], realAddr.Port())
	return buf
}

func unmarshalPeerIntro(payload []byte) (netip.Addr, netip.AddrPort, error) {
	if len(payload) < 10 {
		return netip.Addr{}, netip.AddrPort{}, fmt.Errorf("PeerIntro too short")
	}
	var vpnIP4, realIP4 [4]byte
	copy(vpnIP4[:], payload[0:4])
	copy(realIP4[:], payload[4:8])
	port := binary.BigEndian.Uint16(payload[8:10])
	return netip.AddrFrom4(vpnIP4), netip.AddrPortFrom(netip.AddrFrom4(realIP4), port), nil
}

func marshalPeerQueryRpy(vpnIP netip.Addr, realAddr netip.AddrPort) []byte {
	return marshalPeerIntro(vpnIP, realAddr)
}

func marshalData(srcVPN netip.Addr, data []byte) []byte {
	buf := make([]byte, 4+len(data))
	copy(buf[0:4], srcVPN.AsSlice())
	copy(buf[4:], data)
	return buf
}

func unmarshalData(payload []byte) (netip.Addr, []byte, error) {
	if len(payload) < 4 {
		return netip.Addr{}, nil, fmt.Errorf("Data too short")
	}
	var ip4 [4]byte
	copy(ip4[:], payload[0:4])
	return netip.AddrFrom4(ip4), payload[4:], nil
}

func marshalRelayData(srcVPN, targetVPN netip.Addr, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	copy(buf[0:4], srcVPN.AsSlice())
	copy(buf[4:8], targetVPN.AsSlice())
	copy(buf[8:], data)
	return buf
}

func unmarshalRelayData(payload []byte) (netip.Addr, netip.Addr, []byte, error) {
	if len(payload) < 8 {
		return netip.Addr{}, netip.Addr{}, nil, fmt.Errorf("RelayData too short")
	}
	var srcIP4, targetIP4 [4]byte
	copy(srcIP4[:], payload[0:4])
	copy(targetIP4[:], payload[4:8])
	return netip.AddrFrom4(srcIP4), netip.AddrFrom4(targetIP4), payload[8:], nil
}

func marshalKeepAlive(vpnIP netip.Addr) []byte {
	return vpnIP.AsSlice()
}

func unmarshalKeepAlive(payload []byte) (netip.Addr, error) {
	if len(payload) < 4 {
		return netip.Addr{}, fmt.Errorf("KeepAlive too short")
	}
	var ip4 [4]byte
	copy(ip4[:], payload[0:4])
	return netip.AddrFrom4(ip4), nil
}

func normalizeAddr(addr netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
}

func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "use of closed network connection"
}
