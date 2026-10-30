package server

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"nz/internal/identity"
	"nz/internal/protocol"
	"nz/internal/state"
)

type signedClient struct {
	conn *net.UDPConn
	id   *identity.Identity
	ip   netip.Addr
}

func (e *testEnv) newSignedClient(t *testing.T, user, machine string) *signedClient {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &signedClient{conn: conn, id: id}
	reply := e.joinAs(t, conn, user, machine, id.PublicBlob())
	if err := e.st.Allow(user, ""); err != nil {
		t.Fatalf("allow user: %v", err)
	}
	if err := e.st.Allow(user, machine); err != nil {
		t.Fatalf("allow machine: %v", err)
	}
	ip, err := e.st.AllocateIP("192.168.200.0/24")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	e.st.SetMachineIP(user, machine, ip)
	c.ip = netip.MustParseAddr(ip)
	_ = reply
	c.keepAlive(t, e)
	return c
}

func (e *testEnv) joinAs(t *testing.T, conn *net.UDPConn, user, machine string, pub []byte) protocol.JoinReply {
	t.Helper()
	conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{
		Type: protocol.TypeJoinRequest,
		Body: protocol.EncodeJSON(protocol.JoinRequest{
			User: user, Machine: machine, Public: pub,
			Salt: []byte("salt"), Verifier: []byte("verifier"),
		}),
	}), e.addr)
	buf := make([]byte, 65536)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := conn.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatalf("join reply: %v", err)
	}
	env, _ := protocol.Unmarshal(buf[:n])
	var reply protocol.JoinReply
	protocol.DecodeJSON(env.Body, &reply)
	return reply
}

func (c *signedClient) keepAlive(t *testing.T, e *testEnv) {
	t.Helper()
	msg := protocol.SignedMessage{Type: protocol.TypeKeepAlive, SrcIP: c.ip}
	body := protocol.MarshalSigned(msg, c.id.Sign)
	c.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: protocol.TypeKeepAlive, Body: body}), e.addr)
}

func (c *signedClient) sendRelay(t *testing.T, e *testEnv, dst netip.Addr) {
	t.Helper()
	cipher, err := protocol.Seal(make([]byte, 32), []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	msg := protocol.SignedMessage{Type: protocol.TypeRelayData, SrcIP: c.ip, DstIP: dst, Payload: cipher}
	body := protocol.MarshalSigned(msg, c.id.Sign)
	c.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: protocol.TypeRelayData, Body: body}), e.addr)
}

func (c *signedClient) tryRecv(t *testing.T) bool {
	t.Helper()
	buf := make([]byte, 65536)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(deadline)
		n, _, err := c.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return false
		}
		env, err := protocol.Unmarshal(buf[:n])
		if err != nil || env.Type == protocol.TypeKeepAlive {
			continue
		}
		return true
	}
	return false
}

func TestRelayForwardedForAdmin(t *testing.T) {
	e := newTestEnv(t)
	adminM := e.newSignedClient(t, "me", "desktop")
	other := e.newSignedClient(t, "bob", "pc")
	adminM.sendRelay(t, e, other.ip)
	if !other.tryRecv(t) {
		t.Errorf("admin relay not forwarded")
	}
}

func TestRelayDeniedAcrossUsers(t *testing.T) {
	e := newTestEnv(t)
	other := e.newSignedClient(t, "bob", "pc")
	friend := e.newSignedClient(t, "friend", "laptop")
	friend.sendRelay(t, e, other.ip)
	if other.tryRecv(t) {
		t.Errorf("cross user relay must be dropped")
	}
	if err := e.st.Trust("friend", "laptop"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	friend.sendRelay(t, e, e.srv.serverIP())
	if friend.tryRecv(t) {
		t.Errorf("no reply expected")
	}
}

func TestForgedSourceDropped(t *testing.T) {
	e := newTestEnv(t)
	adminM := e.newSignedClient(t, "me", "desktop")
	other := e.newSignedClient(t, "bob", "pc")
	forged := e.newSignedClient(t, "friend", "laptop")

	cipher, _ := protocol.Seal(make([]byte, 32), []byte("forged"))
	msg := protocol.SignedMessage{Type: protocol.TypeRelayData, SrcIP: adminM.ip, DstIP: other.ip, Payload: cipher}
	body := protocol.MarshalSigned(msg, forged.id.Sign)
	forged.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: protocol.TypeRelayData, Body: body}), e.addr)
	if other.tryRecv(t) {
		t.Errorf("forged source packet must be dropped")
	}
}

func TestPeerQueryHidesAddressOnOneWay(t *testing.T) {
	e := newTestEnv(t)
	adminM := e.newSignedClient(t, "me", "desktop")
	friend := e.newSignedClient(t, "friend", "laptop")

	answer := e.queryPeerAs(t, friend, adminM.ip)
	if !answer.Found {
		t.Fatalf("peer not found")
	}
	if answer.HasAddr {
		t.Errorf("one way pair must not get addresses")
	}
	if err := e.st.Trust("friend", "laptop"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	answer = e.queryPeerAs(t, friend, adminM.ip)
	if !answer.HasAddr {
		t.Errorf("trusted pair is two way, address must be shared")
	}
}

func (e *testEnv) queryPeerAs(t *testing.T, c *signedClient, target netip.Addr) protocol.PeerAnswer {
	t.Helper()
	msg := protocol.SignedMessage{
		Type:    protocol.TypePeerQuery,
		SrcIP:   c.ip,
		DstIP:   e.srv.serverIP(),
		Payload: protocol.PeerQueryPayload(target),
	}
	body := protocol.MarshalSigned(msg, c.id.Sign)
	c.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: protocol.TypePeerQuery, Body: body}), e.addr)
	buf := make([]byte, 65536)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(deadline)
		n, _, err := c.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			t.Fatalf("peer query reply: %v", err)
		}
		env, _ := protocol.Unmarshal(buf[:n])
		if env.Type != protocol.TypePeerQueryRpy {
			continue
		}
		signed, _, err := protocol.ParseSigned(env.Body)
		if err != nil {
			t.Fatalf("parse signed: %v", err)
		}
		answer, err := protocol.ParsePeerAnswer(signed.Payload)
		if err != nil {
			t.Fatalf("parse answer: %v", err)
		}
		return answer
	}
	t.Fatal("peer query reply timeout")
	return protocol.PeerAnswer{}
}

var _ = state.MachineActive
