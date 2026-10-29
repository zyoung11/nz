package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"nz/internal/config"
	"nz/internal/identity"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/state"
)

type Server struct {
	cfg  *config.Config
	st   *state.State
	conn *net.UDPConn

	mu        sync.Mutex
	nonces    map[string][]byte
	endpoints map[string]netip.AddrPort
}

// New creates a server for the given config and state.
func New(cfg *config.Config, st *state.State) *Server {
	return &Server{
		cfg:       cfg,
		st:        st,
		nonces:    make(map[string][]byte),
		endpoints: make(map[string]netip.AddrPort),
	}
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
			keyString(req.Public), identity.Fingerprint(req.Public), req.Salt, req.Verifier)
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
		keyString(msg.Public), identity.Fingerprint(msg.Public), nil, nil)
	if err != nil {
		s.sendJoinReply(remote, protocol.JoinReply{
			Result: protocol.ResultRejected, Error: err.Error(),
		})
		return
	}
	reply := protocol.JoinReply{Result: status}
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
	logx.Success("approval pushed to %s/%s", user, machine)
}

func (s *Server) rememberEndpoint(user, machine string, addr netip.AddrPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endpoints[joinKey(user, machine)] = addr
}

func (s *Server) sendJoinReply(addr netip.AddrPort, reply protocol.JoinReply) {
	s.sendEnvelope(addr, protocol.TypeJoinReply, protocol.EncodeJSON(reply))
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
