package daemon

import (
	"bufio"
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
	"nz/internal/ipc"
	"nz/internal/logx"
	"nz/internal/node"
	"nz/internal/protocol"
	"nz/internal/server"
	"nz/internal/state"
	"nz/internal/tun"
)

type statusData struct {
	Version  string `json:"version"`
	Conn     string `json:"conn,omitempty"`
	VPNIP    string `json:"vpn_ip,omitempty"`
	Mode     string `json:"mode"`
	Port     int    `json:"port"`
	Subnet   string `json:"subnet"`
	Uptime   int64  `json:"uptime_sec"`
	Users    int    `json:"users"`
	Machines int    `json:"machines"`
	Pending  int    `json:"pending"`
}

type Daemon struct {
	Version    string
	cfgPath    string
	cfg        *config.Config
	st         *state.State
	id         *identity.Identity
	srv        *server.Server
	nodeCancel context.CancelFunc
	ln         net.Listener
	startedAt  time.Time
	stopCh     chan struct{}
	stopOnce   sync.Once
}

// New loads config and state for the daemon at cfgPath.
func New(cfgPath, version string) (*Daemon, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	statePath := filepath.Join(filepath.Dir(cfgPath), config.StateFileName)
	st, err := state.Load(statePath)
	if err != nil {
		return nil, err
	}
	id, err := loadOrCreateIdentity(st)
	if err != nil {
		return nil, err
	}
	d := &Daemon{
		Version:   version,
		cfgPath:   cfgPath,
		cfg:       cfg,
		st:        st,
		id:        id,
		startedAt: time.Now(),
		stopCh:    make(chan struct{}),
	}
	if cfg.Mode == "server" {
		salt := identity.UserSalt(cfg.Admin.Name)
		verifier := identity.Verifier(cfg.Admin.Password, salt)
		if err := st.EnsureAdmin(cfg.Admin.Name, salt, verifier); err != nil {
			return nil, err
		}
		d.srv = server.New(cfg, st, id)
		if subnet, err := netip.ParsePrefix(cfg.Subnet); err == nil {
			dev, err := tun.Create(config.TunName, subnet.Addr().Next(), subnet)
			if err != nil {
				logx.Warn("local tun not available: %v", err)
			} else {
				d.srv.SetTUN(dev)
			}
		}
	}
	return d, nil
}

// Serve opens the admin socket and answers requests until stopped.
// On success it notifies pingback when the address is not empty.
func (d *Daemon) Serve(pingback, token string) error {
	ln, err := ipc.Listen(ipc.SocketPath())
	if err != nil {
		return err
	}
	d.ln = ln
	defer os.Remove(ipc.SocketPath())
	logx.Success("daemon started, mode %s, port %d", d.cfg.Mode, d.cfg.Port)
	if d.srv != nil {
		go func() {
			if err := d.srv.Run(); err != nil {
				logx.Error("server loop stopped: %v", err)
			}
		}()
	}
	if d.cfg.Mode == "node" {
		d.startNode()
	}
	if pingback != "" {
		notifyPingback(pingback, token)
	}
	go ipc.Serve(ln, d.handle)
	<-d.stopCh
	ln.Close()
	logx.Info("daemon stopped")
	return nil
}

func notifyPingback(addr, token string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(conn, "%s\n", token)
}

var readOnlyCommands = map[string]bool{
	"status": true,
	"list":   true,
	"users":  true,
}

func (d *Daemon) handle(uid uint32, req ipc.Request) ipc.Response {
	if !readOnlyCommands[req.Command] && uid != 0 {
		return ipc.Response{OK: false, Error: "this command needs root, run with sudo"}
	}
	switch req.Command {
	case "stop":
		return d.handleStop()
	case "reload":
		return d.handleReload()
	case "status":
		return d.handleStatus()
	case "list":
		return d.handleList()
	case "users":
		return d.handleUsers()
	case "allow", "deny", "block", "unblock", "trust", "untrust":
		return d.handleAdmin(req)
	default:
		return ipc.Response{OK: false, Error: fmt.Sprintf("unknown command %s", req.Command)}
	}
}

// Stop terminates the daemon loop.
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		if d.nodeCancel != nil {
			d.nodeCancel()
		}
		close(d.stopCh)
	})
}

func (d *Daemon) handleStop() ipc.Response {
	d.Stop()
	return ipc.Response{OK: true}
}

// checkReloadable rejects config changes that need a service restart.
func (d *Daemon) checkReloadable(cfg *config.Config) error {
	if cfg.Mode != d.cfg.Mode {
		return fmt.Errorf("mode change needs a service reinstall")
	}
	if cfg.Mode == "node" {
		return nil
	}
	if cfg.Port != d.cfg.Port {
		return fmt.Errorf("port change needs a service restart")
	}
	if cfg.Subnet != d.cfg.Subnet {
		return fmt.Errorf("subnet change needs a service restart")
	}
	if cfg.Admin.Name != d.cfg.Admin.Name {
		return fmt.Errorf("admin.name change needs a service restart")
	}
	return nil
}

func (d *Daemon) startNode() {
	ctx, cancel := context.WithCancel(context.Background())
	d.nodeCancel = cancel
	cfg := d.cfg
	go func() {
		if err := node.Run(ctx, cfg, d.st, d.id); err != nil {
			logx.Error("node stopped: %v", err)
			d.Stop()
		}
	}()
}

func (d *Daemon) handleReload() ipc.Response {
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		return ipc.Response{OK: false, Error: fmt.Sprintf("config rejected, keep running with old config: %v", err)}
	}
	if err := cfg.Validate(); err != nil {
		return ipc.Response{OK: false, Error: fmt.Sprintf("config rejected, keep running with old config: %v", err)}
	}
	if err := d.checkReloadable(cfg); err != nil {
		return ipc.Response{OK: false, Error: fmt.Sprintf("config rejected, keep running with old config: %v", err)}
	}
	if cfg.Mode == "server" && cfg.Admin.Password != d.cfg.Admin.Password {
		salt := identity.UserSalt(cfg.Admin.Name)
		if err := d.st.SetVerifier(cfg.Admin.Name, salt, identity.Verifier(cfg.Admin.Password, salt)); err != nil {
			return ipc.Response{OK: false, Error: fmt.Sprintf("config rejected, keep running with old config: %v", err)}
		}
	}
	d.cfg = cfg
	if d.srv != nil {
		d.srv.SetConfig(cfg)
	}
	if d.cfg.Mode == "node" {
		if d.nodeCancel != nil {
			d.nodeCancel()
		}
		d.startNode()
	}
	logx.Success("config reloaded")
	return ipc.Response{OK: true}
}

func (d *Daemon) handleStatus() ipc.Response {
	users, machines := d.st.Snapshot()
	pending := 0
	for _, u := range users {
		if u.Status == state.UserPending {
			pending++
		}
	}
	for _, m := range machines {
		if m.Status == state.MachinePending {
			pending++
		}
	}
	data := statusData{
		Version:  d.Version,
		Conn:     connStateOf(d),
		VPNIP:    connIPOf(d),
		Mode:     d.cfg.Mode,
		Port:     d.cfg.Port,
		Subnet:   d.cfg.Subnet,
		Uptime:   int64(time.Since(d.startedAt).Seconds()),
		Users:    len(users),
		Machines: len(machines),
		Pending:  pending,
	}
	return jsonData(data)
}

func (d *Daemon) handleList() ipc.Response {
	if d.cfg.Mode == "node" {
		peers, err := node.QueryMachines()
		if err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return jsonData(peers)
	}
	_, machines := d.st.Snapshot()
	peers := make([]protocol.PeerInfo, 0, len(machines)+1)
	peers = append(peers, protocol.PeerInfo{
		User:    d.cfg.Admin.Name,
		Machine: "server",
		VPNIP:   serverIP(d),
		Status:  "online",
		Trusted: true,
		Self:    d.cfg.Mode == "server",
	})
	for _, m := range machines {
		status := "unknown"
		if !m.LastSeen.IsZero() {
			status = "offline"
			if time.Since(m.LastSeen) < 15*time.Second {
				status = "online"
			}
		}
		peers = append(peers, protocol.PeerInfo{
			User:    m.User,
			Machine: m.Name,
			VPNIP:   m.VPNIP,
			Trusted: m.Trusted || m.User == d.cfg.Admin.Name,
			Status:  status,
		})
	}
	return jsonData(peers)
}

func (d *Daemon) handleUsers() ipc.Response {
	if d.cfg.Mode == "node" {
		reply, err := node.AdminCommand("users", "")
		if err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		if !reply.OK {
			return ipc.Response{OK: false, Error: reply.Error}
		}
		return ipc.Response{OK: true, Data: reply.Data}
	}
	data, err := d.srv.UsersData()
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	return ipc.Response{OK: true, Data: data}
}

func (d *Daemon) handleAdmin(req ipc.Request) ipc.Response {
	if d.cfg.Mode == "node" {
		reply, err := node.AdminCommand(req.Command, req.Target)
		if err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		if !reply.OK {
			return ipc.Response{OK: false, Error: reply.Error}
		}
		return ipc.Response{OK: true}
	}
	if err := d.srv.AdminCommand(req.Command, req.Target); err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	logx.Success("%s %s applied", req.Command, req.Target)
	return ipc.Response{OK: true}
}

func loadOrCreateIdentity(st *state.State) (*identity.Identity, error) {
	if st.Identity != nil {
		return identity.FromParts(
			st.Identity.SignPublic, st.Identity.SignPrivate,
			st.Identity.ExchPublic, st.Identity.ExchPrivate)
	}
	id, err := identity.Generate()
	if err != nil {
		return nil, err
	}
	sp, spr, ep, epr := id.Parts()
	st.Identity = &state.IdentityData{
		SignPublic:  sp,
		SignPrivate: spr,
		ExchPublic:  ep,
		ExchPrivate: epr,
	}
	if err := st.Save(); err != nil {
		return nil, err
	}
	return id, nil
}

func serverIP(d *Daemon) string {
	prefix, err := netip.ParsePrefix(d.cfg.Subnet)
	if err != nil {
		return ""
	}
	return prefix.Addr().Next().String()
}

func connStateOf(d *Daemon) string {
	if d.cfg.Mode != "node" {
		return ""
	}
	state, _ := node.ConnectionState()
	return state
}

func connIPOf(d *Daemon) string {
	if d.cfg.Mode != "node" {
		return ""
	}
	_, ip := node.ConnectionState()
	return ip
}

func jsonData(v any) ipc.Response {
	data, err := json.Marshal(v)
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	return ipc.Response{OK: true, Data: string(data)}
}

// CheckSocket reports whether a daemon answers on the admin socket.
func CheckSocket() bool {
	conn, err := ipc.Dial(ipc.SocketPath())
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ReadPingbackToken reads one token line from conn.
func ReadPingbackToken(conn net.Conn) string {
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ""
	}
	return line[:len(line)-1]
}
