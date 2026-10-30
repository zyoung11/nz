package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"nz/internal/config"
	"nz/internal/identity"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/tun"
)

var errFatal = errors.New("fatal")

const (
	joinRetryDelay    = 5 * time.Second
	joinTimeout       = 5 * time.Second
	peerQueryTimeout  = 3 * time.Second
	keepAliveInterval = 5 * time.Second
	serverStaleAfter  = 15 * time.Second
)

var (
	connState  atomic.Value
	connVPNIP  atomic.Value
	activeNode atomic.Pointer[Node]
)

// ConnectionState reports the join state and VPN address of the node.
func ConnectionState() (string, string) {
	state, _ := connState.Load().(string)
	ip, _ := connVPNIP.Load().(string)
	return state, ip
}

func setConnState(state, ip string) {
	connState.Store(state)
	connVPNIP.Store(ip)
}

type authData struct {
	ServerSignPub []byte `json:"server_sign_pub,omitempty"`
	ServerExchPub []byte `json:"server_exch_pub,omitempty"`
}

type peer struct {
	signPub     []byte
	exchPub     []byte
	key         []byte
	realAddr    netip.AddrPort
	canCall     bool
	canCallBack bool
	lastSeen    time.Time
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
	joinReplyCh    chan protocol.Envelope
	approveCh      chan protocol.Envelope
	listReplyCh    chan protocol.Envelope
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
	auth, err := loadAuth(authPathOf(keyPath))
	if err != nil {
		return err
	}
	setConnState("connecting", "")
	n := &Node{
		cfg:          cfg,
		id:           id,
		auth:         auth,
		authPath:     authPathOf(keyPath),
		peers:        make(map[netip.Addr]*peer),
		queryReplyCh: make(chan protocol.Envelope, 1),
		joinReplyCh:  make(chan protocol.Envelope, 1),
		approveCh:    make(chan protocol.Envelope, 1),
		listReplyCh:  make(chan protocol.Envelope, 1),
	}
	activeNode.Store(n)
	if err := n.connect(); err != nil {
		return err
	}
	go n.readFromUDP()
	if err := n.join(); err != nil {
		return err
	}
	return n.serve()
}

func authPathOf(keyPath string) string {
	return filepath.Join(filepath.Dir(keyPath), "n0.auth.json")
}

func loadAuth(path string) (*authData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &authData{}, nil
		}
		return nil, fmt.Errorf("failed to read auth file %s: %w", path, err)
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

// verifier derives the user verifier, identical on every machine of
// the same user because the salt depends on the user name only.
func (n *Node) verifier() []byte {
	return identity.Verifier(n.cfg.Password, identity.UserSalt(n.cfg.User))
}

func (n *Node) connect() error {
	serverAddr, err := resolve(n.cfg.Domain, n.cfg.Port)
	if err != nil {
		return err
	}
	n.serverAddr = serverAddr
	logx.Info("server %s resolved to %v", n.cfg.Domain, serverAddr)
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
			if errors.Is(err, errFatal) {
				return err
			}
			logx.Warn("join failed via %v: %v", n.serverAddr, err)
			time.Sleep(joinRetryDelay)
			continue
		}
		switch result {
		case protocol.ResultActive:
			return nil
		case protocol.ResultPending:
			ok, err := n.waitForApproval()
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		default:
			return fmt.Errorf("join rejected by server")
		}
	}
}

func (n *Node) tryJoin() (string, error) {
	verifier := n.verifier()
	req := protocol.JoinRequest{
		User:     n.cfg.User,
		Machine:  n.cfg.Name,
		Public:   n.id.PublicBlob(),
		Salt:     identity.UserSalt(n.cfg.User),
		Verifier: verifier,
	}
	n.sendEnvelope(protocol.TypeJoinRequest, protocol.EncodeJSON(req))
	env, err := waitEnvelope(n.joinReplyCh, joinTimeout)
	if err != nil {
		return "", err
	}
	if env.Type == protocol.TypeJoinChallenge {
		var challenge protocol.JoinChallenge
		if err := protocol.DecodeJSON(env.Body, &challenge); err != nil {
			return "", err
		}
		proof := protocol.Proof(verifier, challenge.Nonce,
			n.cfg.User, n.cfg.Name, n.id.PublicBlob())
		n.sendEnvelope(protocol.TypeJoinProof, protocol.EncodeJSON(protocol.JoinProof{
			User:    n.cfg.User,
			Machine: n.cfg.Name,
			Public:  n.id.PublicBlob(),
			Proof:   proof,
		}))
		env, err = waitEnvelope(n.joinReplyCh, joinTimeout)
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
	if len(serverPub) != identity.PublicBlobSize {
		return
	}
	signPub, exchPub, err := identity.SplitPublic(serverPub)
	if err != nil {
		return
	}
	if bytes.Equal(n.auth.ServerSignPub, signPub) {
		return
	}
	if len(n.auth.ServerSignPub) > 0 {
		logx.Warn("server key changed, updating the stored key")
	}
	n.auth.ServerSignPub = signPub
	n.auth.ServerExchPub = exchPub
	n.mu.Lock()
	delete(n.peers, serverAddrOf(n))
	n.mu.Unlock()
	if err := n.saveAuth(); err != nil {
		logx.Warn("%v", err)
	}
}

func (n *Node) adoptVPNIP(ipStr string) error {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return fmt.Errorf("%w: bad vpn ip %q", errFatal, ipStr)
	}
	n.vpnIP = ip
	n.subnet = netip.PrefixFrom(netip.AddrFrom4([4]byte{ip.As4()[0], ip.As4()[1], ip.As4()[2], 0}), 24)
	if n.tun != nil {
		return nil
	}
	dev, err := tun.Create(config.TunName, ip, n.subnet)
	if err != nil {
		return fmt.Errorf("%w: %w", errFatal, err)
	}
	n.tun = dev
	setConnState("joined", ip.String())
	logx.Success("machine %s joined as %s", n.cfg.Name, ip)
	return nil
}

func (n *Node) waitForApproval() (bool, error) {
	logx.Info("waiting for approval, retry every %s", joinRetryDelay)
	env, err := waitEnvelope(n.approveCh, joinRetryDelay)
	if err != nil {
		return false, nil
	}
	var notify protocol.ApproveNotify
	if err := protocol.DecodeJSON(env.Body, &notify); err != nil {
		return false, nil
	}
	if err := n.adoptVPNIP(notify.VPNIP); err != nil {
		return false, err
	}
	logx.Success("approval received")
	return true, nil
}

func (n *Node) sendEnvelope(typ protocol.MsgType, body []byte) {
	n.conn.WriteToUDPAddrPort(protocol.Marshal(protocol.Envelope{Type: typ, Body: body}), n.serverAddr)
}

func waitEnvelope(ch chan protocol.Envelope, timeout time.Duration) (protocol.Envelope, error) {
	select {
	case env := <-ch:
		return env, nil
	case <-time.After(timeout):
		return protocol.Envelope{}, fmt.Errorf("timeout waiting for server")
	}
}
