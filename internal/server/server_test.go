package server

import (
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"nz/internal/config"
	"nz/internal/identity"
	"nz/internal/protocol"
	"nz/internal/state"
)

type testEnv struct {
	srv    *Server
	st     *state.State
	conn   *net.UDPConn
	client *net.UDPConn
	addr   netip.AddrPort
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "n0-state.json")
	st, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	cfg := &config.Config{
		Mode:   "server",
		Port:   0,
		Subnet: "192.168.200.0/24",
		Admin:  config.Account{Name: "me", Password: "pw"},
	}
	srv := New(cfg, st)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.ServeConn(conn)
	t.Cleanup(func() { conn.Close() })

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return &testEnv{
		srv:    srv,
		st:     st,
		conn:   conn,
		client: client,
		addr:   netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"),
			uint16(conn.LocalAddr().(*net.UDPAddr).Port)),
	}
}

func (e *testEnv) send(t *testing.T, env protocol.Envelope) {
	t.Helper()
	e.client.WriteToUDPAddrPort(protocol.Marshal(env), e.addr)
}

func (e *testEnv) recv(t *testing.T) protocol.Envelope {
	t.Helper()
	buf := make([]byte, 65536)
	e.client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := e.client.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	env, err := protocol.Unmarshal(buf[:n])
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

func (e *testEnv) join(t *testing.T, user, machine string, pub []byte) protocol.JoinReply {
	t.Helper()
	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinRequest,
		Body: protocol.EncodeJSON(protocol.JoinRequest{
			User: user, Machine: machine, Public: pub,
			Salt: []byte("salt"), Verifier: []byte("verifier"),
		}),
	})
	return e.recvJoinReply(t)
}

func (e *testEnv) joinExisting(t *testing.T, user, machine string, pub, verifier []byte) protocol.JoinReply {
	t.Helper()
	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinRequest,
		Body: protocol.EncodeJSON(protocol.JoinRequest{
			User: user, Machine: machine, Public: pub,
		}),
	})
	env := e.recv(t)
	if env.Type == protocol.TypeJoinChallenge {
		var challenge protocol.JoinChallenge
		protocol.DecodeJSON(env.Body, &challenge)
		proof := protocol.Proof(verifier, challenge.Nonce, user, machine, pub)
		e.send(t, protocol.Envelope{
			Type: protocol.TypeJoinProof,
			Body: protocol.EncodeJSON(protocol.JoinProof{
				User: user, Machine: machine, Public: pub, Proof: proof,
			}),
		})
		env = e.recv(t)
	}
	if env.Type != protocol.TypeJoinReply {
		t.Fatalf("reply type %02x, want join reply", env.Type)
	}
	var reply protocol.JoinReply
	protocol.DecodeJSON(env.Body, &reply)
	return reply
}

func (e *testEnv) recvJoinReply(t *testing.T) protocol.JoinReply {
	t.Helper()
	env := e.recv(t)
	if env.Type != protocol.TypeJoinReply {
		t.Fatalf("reply type %02x, want join reply", env.Type)
	}
	var reply protocol.JoinReply
	protocol.DecodeJSON(env.Body, &reply)
	return reply
}

func TestJoinNewUserPending(t *testing.T) {
	e := newTestEnv(t)
	pub := []byte("public-key-1")
	reply := e.join(t, "friend", "laptop", pub)
	if reply.Result != protocol.ResultPending {
		t.Fatalf("result %q, want pending", reply.Result)
	}
	u, ok := e.st.GetUser("friend")
	if !ok || u.Status != state.UserPending {
		t.Errorf("user friend not pending: %+v", u)
	}
	m, ok := e.st.GetMachine("friend", "laptop")
	if !ok || m.Status != state.MachinePending {
		t.Errorf("machine not pending: %+v", m)
	}
}

func TestApprovePush(t *testing.T) {
	e := newTestEnv(t)
	pub := []byte("public-key-2")
	e.join(t, "friend", "laptop", pub)
	if err := e.st.Allow("friend", ""); err != nil {
		t.Fatalf("allow user: %v", err)
	}
	if err := e.st.Allow("friend", "laptop"); err != nil {
		t.Fatalf("allow machine: %v", err)
	}
	ip, err := e.st.AllocateIP("192.168.200.0/24")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if err := e.st.SetMachineIP("friend", "laptop", ip); err != nil {
		t.Fatalf("set ip: %v", err)
	}
	e.srv.NotifyApprove("friend", "laptop")
	env := e.recv(t)
	if env.Type != protocol.TypeApproveNotify {
		t.Fatalf("type %02x, want approve notify", env.Type)
	}
	var notify protocol.ApproveNotify
	protocol.DecodeJSON(env.Body, &notify)
	if notify.VPNIP != ip {
		t.Errorf("notify ip %q, want %q", notify.VPNIP, ip)
	}
}

func TestJoinExistingUserNeedsProof(t *testing.T) {
	e := newTestEnv(t)
	pub := []byte("public-key-3")
	e.join(t, "friend", "laptop", pub)
	if err := e.st.Allow("friend", ""); err != nil {
		t.Fatalf("allow user: %v", err)
	}

	salt, _ := identity.NewSalt()
	verifier := identity.Verifier("pw", salt)
	if err := e.st.SetVerifier("friend", salt, verifier); err != nil {
		t.Fatalf("set verifier: %v", err)
	}

	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinRequest,
		Body: protocol.EncodeJSON(protocol.JoinRequest{
			User: "friend", Machine: "desktop", Public: []byte("public-key-4"),
		}),
	})
	env := e.recv(t)
	if env.Type != protocol.TypeJoinChallenge {
		t.Fatalf("type %02x, want challenge", env.Type)
	}
	var challenge protocol.JoinChallenge
	protocol.DecodeJSON(env.Body, &challenge)

	proof := protocol.Proof(verifier, challenge.Nonce, "friend", "desktop", []byte("public-key-4"))
	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinProof,
		Body: protocol.EncodeJSON(protocol.JoinProof{
			User: "friend", Machine: "desktop", Public: []byte("public-key-4"), Proof: proof,
		}),
	})
	env = e.recv(t)
	var reply protocol.JoinReply
	protocol.DecodeJSON(env.Body, &reply)
	if reply.Result != protocol.ResultPending {
		t.Fatalf("result %q, want pending", reply.Result)
	}
	if _, ok := e.st.GetMachine("friend", "desktop"); !ok {
		t.Errorf("machine desktop not registered")
	}
}

func TestJoinRejectsBadProof(t *testing.T) {
	e := newTestEnv(t)
	e.join(t, "friend", "laptop", []byte("public-key-5"))
	e.st.Allow("friend", "")
	salt, _ := identity.NewSalt()
	verifier := identity.Verifier("pw", salt)
	e.st.SetVerifier("friend", salt, verifier)

	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinRequest,
		Body: protocol.EncodeJSON(protocol.JoinRequest{
			User: "friend", Machine: "evil", Public: []byte("public-key-6"),
		}),
	})
	env := e.recv(t)
	var challenge protocol.JoinChallenge
	protocol.DecodeJSON(env.Body, &challenge)
	e.send(t, protocol.Envelope{
		Type: protocol.TypeJoinProof,
		Body: protocol.EncodeJSON(protocol.JoinProof{
			User: "friend", Machine: "evil", Public: []byte("public-key-6"),
			Proof: []byte("forged"),
		}),
	})
	env = e.recv(t)
	var reply protocol.JoinReply
	protocol.DecodeJSON(env.Body, &reply)
	if reply.Result != protocol.ResultRejected {
		t.Fatalf("result %q, want rejected", reply.Result)
	}
	if _, ok := e.st.GetMachine("friend", "evil"); ok {
		t.Errorf("forged machine got registered")
	}
}

func TestMachineNameConflict(t *testing.T) {
	e := newTestEnv(t)
	e.join(t, "friend", "laptop", []byte("key-a"))
	reply := e.joinExisting(t, "friend", "laptop", []byte("key-b"), []byte("verifier"))
	if reply.Result != protocol.ResultRejected {
		t.Fatalf("result %q, want rejected for duplicate name", reply.Result)
	}
}

func TestPolicyMatrix(t *testing.T) {
	e := newTestEnv(t)
	admin := state.Machine{User: "me", Name: "desktop", Status: state.MachineActive}
	mine := state.Machine{User: "friend", Name: "laptop", Status: state.MachineActive}
	friends := state.Machine{User: "friend", Name: "desktop", Status: state.MachineActive}
	other := state.Machine{User: "bob", Name: "pc", Status: state.MachineActive}
	trusted := state.Machine{User: "bob", Name: "trusted", Status: state.MachineActive, Trusted: true}

	if !e.srv.AllowRelay(admin, other) {
		t.Error("admin must reach everyone")
	}
	if !e.srv.AllowRelay(mine, friends) {
		t.Error("same user machines must reach each other")
	}
	if e.srv.AllowRelay(mine, admin) {
		t.Error("untrusted machine must not reach admin")
	}
	if !e.srv.AllowRelay(trusted, admin) {
		t.Error("trusted machine must reach admin")
	}
	if e.srv.AllowRelay(mine, other) {
		t.Error("different users must not reach each other")
	}
	if e.srv.AllowP2P(mine, admin) {
		t.Error("one-way allowance must force relay")
	}
	if !e.srv.AllowP2P(mine, friends) {
		t.Error("same user pair must allow p2p")
	}
	if e.srv.AllowAddressExchange(mine, admin) {
		t.Error("one-way pair must not exchange addresses")
	}
}

func TestAllocateIPSkipsUsed(t *testing.T) {
	e := newTestEnv(t)
	e.join(t, "friend", "laptop", []byte("k1"))
	e.st.Allow("friend", "")
	e.st.Allow("friend", "laptop")
	first, err := e.st.AllocateIP("192.168.200.0/24")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if first != "192.168.200.2" {
		t.Errorf("first ip %q, want 192.168.200.2", first)
	}
	e.st.SetMachineIP("friend", "laptop", first)
	e.joinExisting(t, "friend", "desktop", []byte("k2"), []byte("verifier"))
	e.st.Allow("friend", "desktop")
	second, err := e.st.AllocateIP("192.168.200.0/24")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if second != "192.168.200.3" {
		t.Errorf("second ip %q, want 192.168.200.3", second)
	}
}
