package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nz/internal/config"
	"nz/internal/identity"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/tun"
)

const (
	joinRetryDelay    = 5 * time.Second
	joinTimeout       = 5 * time.Second
	peerQueryTimeout  = 3 * time.Second
	keepAliveInterval = 5 * time.Second
	serverStaleAfter  = 15 * time.Second
)

type authData struct {
	Salt          []byte `json:"salt"`
	Verifier      []byte `json:"verifier"`
	ServerSignPub []byte `json:"server_sign_pub,omitempty"`
	ServerExchPub []byte `json:"server_exch_pub,omitempty"`
}

type peer struct {
	signPub  []byte
	exchPub  []byte
	key      []byte
	realAddr netip.AddrPort
	lastSeen time.Time
}

type Node struct {
	cfg        *config.Config
	id         *identity.Identity
	auth       *authData
	authPath   string
	conn       *net.UDPConn
	serverAddr netip.AddrPort
	vpnIP      netip.Addr
	subnet     netip.Prefix
	tun        tun.Device

	mu             sync.Mutex
	peers          map[netip.Addr]*peer
	queryReplyCh   chan protocol.Envelope
	lastServerSeen time.Time
}

// Run joins the network and forwards traffic until the process stops.
func Run(cfg *config.Config, keyPath string) error {
	if cfg.Mode != "node" {
		return fmt.Errorf("node mode required")
	}
	id, err := identity.LoadOrCreate(keyPath)
	if err != nil {
		return err
	}
	auth, err := loadAuth(authPathOf(keyPath), cfg.Password)
	if err != nil {
		return err
	}
	n := &Node{
		cfg:          cfg,
		id:           id,
		auth:         auth,
		authPath:     authPathOf(keyPath),
		peers:        make(map[netip.Addr]*peer),
		queryReplyCh: make(chan protocol.Envelope, 1),
	}
	if err := n.connect(); err != nil {
		return err
	}
	if err := n.join(); err != nil {
		return err
	}
	return n.serve()
}

func authPathOf(keyPath string) string {
	return filepath.Join(filepath.Dir(keyPath), "n0.auth.json")
}

func loadAuth(path, password string) (*authData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read auth file %s: %w", path, err)
		}
		salt, err := identity.NewSalt()
		if err != nil {
			return nil, err
		}
		return &authData{Salt: salt, Verifier: identity.Verifier(password, salt)}, nil
	}
	var a authData
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("failed to parse auth file %s: %w", path, err)
	}
	return &a, nil
}

func (n *Node) saveAuth() error {
	data, err := json.MarshalIndent(n.auth, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode auth: %w", err)
	}
	if err := os.WriteFile(n.authPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write auth file %s: %w", n.authPath, err)
	}
	return nil
}

func (n *Node) connect() error {
	serverAddr, err := resolve(n.cfg.Domain, n.cfg.Port)
	if err != nil {
		return err
	}
	n.serverAddr = serverAddr
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("failed to listen UDP: %w", err)
	}
	n.conn = conn
	return nil
}

func resolve(host string, port int) (netip.AddrPort, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(addr, uint16(port)), nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(context.TODO(), "ip4", host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("failed to resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return netip.AddrPort{}, fmt.Errorf("no address for %s", host)
	}
	return netip.AddrPortFrom(addrs[0].Unmap(), uint16(port)), nil
}

func (n *Node) join() error {
	for {
		result, err := n.tryJoin()
		if err != nil {
			logx.Warn("join failed: %v", err)
			time.Sleep(joinRetryDelay)
			continue
		}
		switch result {
		case protocol.ResultActive:
			return nil
		case protocol.ResultPending:
			if n.waitForApproval() {
				return nil
			}
		default:
			return fmt.Errorf("join rejected by server")
		}
	}
}

func (n *Node) tryJoin() (string, error) {
	req := protocol.JoinRequest{
		User:    n.cfg.User,
		Machine: n.cfg.Name,
		Public:  n.id.PublicBlob(),
	}
	if len(n.auth.ServerSignPub) == 0 {
		req.Salt = n.auth.Salt
		req.Verifier = n.auth.Verifier
	}
	n.sendEnvelope(protocol.TypeJoinRequest, protocol.EncodeJSON(req))
	env, err := n.recvEnvelope(joinTimeout)
	if err != nil {
		return "", err
	}
	if env.Type == protocol.TypeJoinChallenge {
		var challenge protocol.JoinChallenge
		if err := protocol.DecodeJSON(env.Body, &challenge); err != nil {
			return "", err
		}
		proof := protocol.Proof(n.auth.Verifier, challenge.Nonce,
			n.cfg.User, n.cfg.Name, n.id.PublicBlob())
		n.sendEnvelope(protocol.TypeJoinProof, protocol.EncodeJSON(protocol.JoinProof{
			User:    n.cfg.User,
			Machine: n.cfg.Name,
			Public:  n.id.PublicBlob(),
			Proof:   proof,
		}))
		env, err = n.recvEnvelope(joinTimeout)
		if err != nil {
			return "", err
		}
	}
	if env.Type != protocol.TypeJoinReply {
		return "", fmt.Errorf("unexpected message type %02x", env.Type)
	}
	var reply protocol.JoinReply
	if err := protocol.DecodeJSON(env.Body, &reply); err != nil {
		return "", err
	}
	if reply.Result == protocol.ResultRejected {
		return "", fmt.Errorf("server says: %s", reply.Error)
	}
	n.acceptServerKeys(reply.ServerPub)
	if reply.Result == protocol.ResultActive {
		return protocol.ResultActive, n.adoptVPNIP(reply.VPNIP)
	}
	return reply.Result, nil
}

func (n *Node) acceptServerKeys(serverPub []byte) {
	if len(serverPub) != identity.PublicBlobSize || len(n.auth.ServerSignPub) > 0 {
		return
	}
	signPub, exchPub, err := identity.SplitPublic(serverPub)
	if err != nil {
		return
	}
	n.auth.ServerSignPub = signPub
	n.auth.ServerExchPub = exchPub
	if err := n.saveAuth(); err != nil {
		logx.Warn("%v", err)
	}
}

func (n *Node) adoptVPNIP(ipStr string) error {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return fmt.Errorf("bad vpn ip %q: %w", ipStr, err)
	}
	n.vpnIP = ip
	n.subnet = netip.PrefixFrom(netip.AddrFrom4([4]byte{ip.As4()[0], ip.As4()[1], ip.As4()[2], 0}), 24)
	if n.tun != nil {
		return nil
	}
	dev, err := tun.Create(n.cfg.Name, ip, n.subnet)
	if err != nil {
		return err
	}
	n.tun = dev
	logx.Success("machine %s joined as %s", n.cfg.Name, ip)
	return nil
}

func (n *Node) waitForApproval() bool {
	logx.Info("waiting for approval, retry every %s", joinRetryDelay)
	deadline := time.Now().Add(joinRetryDelay)
	for time.Now().Before(deadline) {
		env, err := n.recvEnvelope(time.Until(deadline))
		if err != nil {
			break
		}
		if env.Type != protocol.TypeApproveNotify {
			continue
		}
		var notify protocol.ApproveNotify
		if err := protocol.DecodeJSON(env.Body, &notify); err != nil {
			continue
		}
		if err := n.adoptVPNIP(notify.VPNIP); err != nil {
			logx.Warn("%v", err)
			continue
		}
		logx.Success("approval received")
		return true
	}
	return false
}

func (n *Node) sendEnvelope(typ protocol.MsgType, body []byte) {
	n.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: typ, Body: body}), n.serverAddr)
}

func (n *Node) recvEnvelope(timeout time.Duration) (protocol.Envelope, error) {
	buf := make([]byte, 65536)
	n.conn.SetReadDeadline(time.Now().Add(timeout))
	defer n.conn.SetReadDeadline(time.Time{})
	for {
		nread, from, err := n.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return protocol.Envelope{}, err
		}
		if normalize(from) != normalize(n.serverAddr) {
			continue
		}
		env, err := protocol.Unmarshal(buf[:nread])
		if err != nil {
			continue
		}
		return env, nil
	}
}

func normalize(addr netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
}
