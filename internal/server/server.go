package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"nz/internal/config"
	"nz/internal/identity"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/state"
	"nz/internal/tun"
)

type Server struct {
	cfg  *config.Config
	st   *state.State
	id   *identity.Identity
	tun  tun.Device
	conn *net.UDPConn

	mu          sync.Mutex
	nonces      map[string][]byte
	endpoints   map[string]netip.AddrPort
	ipEndpoints map[netip.Addr]netip.AddrPort
	flows       map[string]time.Time
}

// New creates a server for the given config, state and identity.
func New(cfg *config.Config, st *state.State, id *identity.Identity) *Server {
	return &Server{
		cfg:         cfg,
		st:          st,
		id:          id,
		nonces:      make(map[string][]byte),
		endpoints:   make(map[string]netip.AddrPort),
		ipEndpoints: make(map[netip.Addr]netip.AddrPort),
		flows:       make(map[string]time.Time),
	}
}

// SetTUN attaches the local TUN device for traffic to the server itself.
func (s *Server) SetTUN(dev tun.Device) {
	s.tun = dev
	go s.readFromTUN()
}

// SetConfig swaps in a reloaded config.
func (s *Server) SetConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

// Run serves the UDP protocol until the socket fails.
func (s *Server) Run() error {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: s.cfg.Port})
	if err != nil {
		return fmt.Errorf("failed to listen on udp %d: %w", s.cfg.Port, err)
	}
	return s.ServeConn(conn)
}

// ServeConn runs the protocol loop on an existing UDP socket.
func (s *Server) ServeConn(conn *net.UDPConn) error {
	s.conn = conn
	logx.Success("server listening on udp %d", conn.LocalAddr().(*net.UDPAddr).Port)
	buf := make([]byte, 65536)
	for {
		n, remote, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return err
		}
		env, err := protocol.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		s.handle(env, remote)
	}
}

func (s *Server) handle(env protocol.Envelope, remote netip.AddrPort) {
	switch env.Type {
	case protocol.TypeJoinRequest:
		s.handleJoinRequest(env.Body, remote)
	case protocol.TypeJoinProof:
		s.handleJoinProof(env.Body, remote)
	case protocol.TypeData, protocol.TypeRelayData, protocol.TypePeerQuery,
		protocol.TypeMachineList, protocol.TypeKeepAlive, protocol.TypeAdminRequest:
		s.handleSigned(env, remote)
	}
}

func (s *Server) handleJoinRequest(body []byte, remote netip.AddrPort) {
	var req protocol.JoinRequest
	if err := protocol.DecodeJSON(body, &req); err != nil {
		return
	}
	if req.User == "" || req.Machine == "" || len(req.Public) == 0 {
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected,
			Error:  "user, machine and public key are required",
		})
		return
	}
	s.rememberEndpoint(req.User, req.Machine, remote)

	if _, found := s.st.GetUser(req.User); !found {
		status, err := s.st.RegisterJoin(req.User, req.Machine,
			keyString(req.Public), identity.Fingerprint(req.Public), req.Salt, req.Verifier, req.DesiredIP)
		if err != nil {
			s.sendJoinReply(remote, protocol.JoinReply{
				Result: protocol.ResultRejected, Error: err.Error(),
			})
			return
		}
		logInfoJoin(req.User, req.Machine, status)
		s.sendJoinReply(remote, protocol.JoinReply{Result: status})
		return
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return
	}
	s.mu.Lock()
	s.nonces[joinKey(req.User, req.Machine)] = nonce
	s.mu.Unlock()
	s.sendEnvelope(remote, protocol.TypeJoinChallenge,
		protocol.EncodeJSON(protocol.JoinChallenge{Nonce: nonce}))
}

func (s *Server) handleJoinProof(body []byte, remote netip.AddrPort) {
	var msg protocol.JoinProof
	if err := protocol.DecodeJSON(body, &msg); err != nil {
		return
	}
	user, found := s.st.GetUser(msg.User)
	if !found {
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected, Error: "unknown user",
		})
		return
	}
	s.mu.Lock()
	nonce := s.nonces[joinKey(msg.User, msg.Machine)]
	delete(s.nonces, joinKey(msg.User, msg.Machine))
	s.mu.Unlock()
	if nonce == nil {
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected, Error: "no challenge to answer",
		})
		return
	}
	verifier := decodeVerifier(user)
	if !protocol.VerifyProof(verifier, msg.Proof, nonce, msg.User, msg.Machine, msg.Public) {
		logx.Warn("join proof rejected for %s/%s", msg.User, msg.Machine)
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected, Error: "proof verification failed",
		})
		return
	}
	s.rememberEndpoint(msg.User, msg.Machine, remote)
	status, err := s.st.RegisterJoin(msg.User, msg.Machine,
		keyString(msg.Public), identity.Fingerprint(msg.Public), nil, nil, msg.DesiredIP)
	if err != nil {
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected, Error: err.Error(),
		})
		return
	}
	reply := protocol.JoinReply{Result: status}
	s.rebindDesired(msg.User, msg.Machine, msg.DesiredIP)
	if status == string(state.MachinePending) {
		if s.autoApproveAdmin(msg.User, msg.Machine) {
			reply.Result = string(state.MachineActive)
		}
	}
	if m, ok := s.st.GetMachine(msg.User, msg.Machine); ok && m.VPNIP != "" {
		reply.VPNIP = m.VPNIP
	}
	logInfoJoin(msg.User, msg.Machine, status)
	s.sendJoinReply(remote, reply)
}

// NotifyApprove pushes an approval to the machine endpoint when known.
func (s *Server) NotifyApprove(user, machine string) {
	m, ok := s.st.GetMachine(user, machine)
	if !ok || m.VPNIP == "" {
		return
	}
	s.mu.Lock()
	addr, known := s.endpoints[joinKey(user, machine)]
	s.mu.Unlock()
	if !known {
		logx.Warn("no endpoint for %s/%s, wait for its next join", user, machine)
		return
	}
	s.sendEnvelope(addr, protocol.TypeApproveNotify,
		protocol.EncodeJSON(protocol.ApproveNotify{VPNIP: m.VPNIP}))
	if ip, err := netip.ParseAddr(m.VPNIP); err == nil {
		s.rememberIPEndpoint(ip, addr)
	}
	logx.Success("approval pushed to %s/%s", user, machine)
}

// AdminCommand executes a management operation locally.
func (s *Server) AdminCommand(command, target string) error {
	user, machine, err := state.ParseTarget(target)
	if err != nil {
		return err
	}
	switch command {
	case "allow":
		if err := s.st.Allow(user, machine); err != nil {
			return err
		}
		if machine != "" {
			return s.approveMachine(user, machine)
		}
		return nil
	case "deny":
		err = s.st.Deny(user, machine)
	case "block":
		err = s.st.Block(user, machine)
	case "unblock":
		err = s.st.Unblock(user, machine)
	case "trust":
		err = s.st.Trust(user, machine)
	case "untrust":
		err = s.st.Untrust(user, machine)
	default:
		return fmt.Errorf("unknown command %s", command)
	}
	if err == nil {
		s.broadcastPolicyChanged()
	}
	return err
}

// broadcastPolicyChanged tells every machine to refresh its cached
// policy so allow and block take effect within one round trip.
func (s *Server) broadcastPolicyChanged() {
	s.mu.Lock()
	targets := make([]netip.AddrPort, 0, len(s.ipEndpoints))
	for _, addr := range s.ipEndpoints {
		targets = append(targets, addr)
	}
	s.mu.Unlock()
	msg := protocol.SignedMessage{
		Type:  protocol.TypePolicyChanged,
		SrcIP: s.serverIP(),
	}
	body := protocol.MarshalSigned(msg, s.id.Sign)
	for _, addr := range targets {
		s.sendRaw(protocol.Envelope{Type: protocol.TypePolicyChanged, Body: body}, addr)
	}
}

func (s *Server) approveMachine(user, machine string) error {
	m, ok := s.st.GetMachine(user, machine)
	if !ok {
		return nil
	}
	if m.VPNIP == "" {
		ip, err := s.st.AllocateIP(s.cfg.Subnet, m.DesiredIP)
		if err != nil {
			return err
		}
		if err := s.st.SetMachineIP(user, machine, ip); err != nil {
			return err
		}
		logx.Info("allocated %s for %s/%s", ip, user, machine)
	}
	s.NotifyApprove(user, machine)
	return nil
}

// UsersData returns the full user and machine table as JSON.
func (s *Server) UsersData() (string, error) {
	users, machines := s.st.Snapshot()
	for i := range machines {
		if machines[i].User == s.cfg.Admin.Name {
			machines[i].Trusted = true
		}
	}
	data, err := json.Marshal(struct {
		Users    []state.User    `json:"users"`
		Machines []state.Machine `json:"machines"`
	}{users, machines})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// rebindDesired moves an active machine when its pinned ip changed.
func (s *Server) rebindDesired(user, machine string, desired int) {
	if desired == 0 {
		return
	}
	m, ok := s.st.GetMachine(user, machine)
	if !ok || m.VPNIP == "" {
		return
	}
	ip, err := netip.ParseAddr(m.VPNIP)
	if err != nil || int(ip.As4()[3]) == desired {
		return
	}
	newIP, err := s.st.AllocateIP(s.cfg.Subnet, desired)
	if err != nil {
		logx.Warn("pin change for %s/%s failed: %v", user, machine, err)
		return
	}
	if err := s.st.SetMachineIP(user, machine, newIP); err == nil {
		logx.Success("%s/%s moved to %s", user, machine, newIP)
	}
}

func (s *Server) desiredOf(user, machine string) int {
	if m, ok := s.st.GetMachine(user, machine); ok {
		return m.DesiredIP
	}
	return 0
}

// autoApproveAdmin activates admin machines without approval and
// allocates their addresses immediately.
func (s *Server) autoApproveAdmin(user, machine string) bool {
	if user != s.adminName() {
		return false
	}
	if err := s.st.Allow(user, machine); err != nil {
		return false
	}
	ip, err := s.st.AllocateIP(s.cfg.Subnet, s.desiredOf(user, machine))
	if err != nil {
		return false
	}
	if err := s.st.SetMachineIP(user, machine, ip); err != nil {
		return false
	}
	logx.Success("admin machine %s/%s approved as %s", user, machine, ip)
	return true
}

func (s *Server) rememberEndpoint(user, machine string, addr netip.AddrPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endpoints[joinKey(user, machine)] = addr
}

func (s *Server) sendJoinReply(addr netip.AddrPort, reply protocol.JoinReply) {
	reply.ServerPub = s.id.PublicBlob()
	if reply.VPNIP != "" {
		if ip, err := netip.ParseAddr(reply.VPNIP); err == nil {
			s.rememberIPEndpoint(ip, addr)
		}
	}
	s.sendEnvelope(addr, protocol.TypeJoinReply, protocol.EncodeJSON(reply))
}

func (s *Server) sendRaw(env protocol.Envelope, addr netip.AddrPort) {
	if s.conn == nil {
		return
	}
	s.conn.WriteToUDPAddrPort(protocol.Marshal(env), addr)
}

func (s *Server) sendEnvelope(addr netip.AddrPort, typ protocol.MsgType, body []byte) {
	if s.conn == nil {
		return
	}
	s.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: typ, Body: body}), addr)
}

func joinKey(user, machine string) string {
	return user + "/" + machine
}

func keyString(pub []byte) string {
	return base64.StdEncoding.EncodeToString(pub)
}

func decodeVerifier(u state.User) []byte {
	verifier, err := decodeB64(u.Verifier)
	if err != nil {
		return nil
	}
	return verifier
}

func logInfoJoin(user, machine, status string) {
	switch status {
	case "pending":
		logx.Info("join request %s/%s waits for approval", user, machine)
	case "rejected":
		logx.Warn("join request %s/%s rejected", user, machine)
	default:
		logx.Success("machine %s/%s joined as %s", user, machine, status)
	}
}
