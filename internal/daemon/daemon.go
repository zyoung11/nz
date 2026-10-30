package daemon

import (
	"bufio"
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
	"nz/internal/server"
	"nz/internal/state"
	"nz/internal/tun"
)

type statusData struct {
	Version  string `json:"version"`
	Mode     string `json:"mode"`
	Port     int    `json:"port"`
	Subnet   string `json:"subnet"`
	Uptime   int64  `json:"uptime_sec"`
	Users    int    `json:"users"`
	Machines int    `json:"machines"`
	Pending  int    `json:"pending"`
}

type Daemon struct {
	Version   string
	cfgPath   string
	keyPath   string
	cfg       *config.Config
	st        *state.State
	srv       *server.Server
	ln        net.Listener
	startedAt time.Time
	stopCh    chan struct{}
	stopOnce  sync.Once
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
	keyPath := filepath.Join(filepath.Dir(cfgPath), config.KeyFileName)
	id, err := identity.LoadOrCreate(keyPath)
	if err != nil {
		return nil, err
	}
	d := &Daemon{
		Version:   version,
		cfgPath:   cfgPath,
		keyPath:   keyPath,
		cfg:       cfg,
		st:        st,
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
		go func() {
			if err := node.Run(d.cfg, d.keyPath); err != nil {
				logx.Error("node stopped: %v", err)
			}
		}()
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

func (d *Daemon) handle(req ipc.Request) ipc.Response {
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
	d.stopOnce.Do(func() { close(d.stopCh) })
}

func (d *Daemon) handleStop() ipc.Response {
	d.Stop()
	return ipc.Response{OK: true}
}

func (d *Daemon) handleReload() ipc.Response {
	cfg, err := config.Load(d.cfgPath)
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	if err := cfg.Validate(); err != nil {
		return ipc.Response{OK: false, Error: fmt.Sprintf("config rejected, keep running with old config: %v", err)}
	}
	d.cfg = cfg
	if d.srv != nil {
		d.srv.SetConfig(cfg)
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
	_, machines := d.st.Snapshot()
	if d.cfg.Mode == "node" {
		filtered := machines[:0:0]
		for _, m := range machines {
			if m.User == d.cfg.User || m.Trusted {
				filtered = append(filtered, m)
			}
		}
		machines = filtered
	}
	return jsonData(machines)
}

func (d *Daemon) handleUsers() ipc.Response {
	if d.cfg.Mode != "server" {
		return ipc.Response{OK: false, Error: "users is only available on the server"}
	}
	users, machines := d.st.Snapshot()
	return jsonData(struct {
		Users    []state.User    `json:"users"`
		Machines []state.Machine `json:"machines"`
	}{users, machines})
}

func (d *Daemon) handleAdmin(req ipc.Request) ipc.Response {
	if d.cfg.Mode != "server" {
		return ipc.Response{OK: false, Error: fmt.Sprintf("%s is only available on the server", req.Command)}
	}
	user, machine, err := state.ParseTarget(req.Target)
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	switch req.Command {
	case "allow":
		err = d.st.Allow(user, machine)
		if err == nil && machine != "" {
			err = d.approveMachine(user, machine)
		}
	case "deny":
		err = d.st.Deny(user, machine)
	case "block":
		err = d.st.Block(user, machine)
	case "unblock":
		err = d.st.Unblock(user, machine)
	case "trust":
		err = d.st.Trust(user, machine)
	case "untrust":
		err = d.st.Untrust(user, machine)
	}
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	logx.Success("%s %s applied", req.Command, req.Target)
	return ipc.Response{OK: true}
}

func (d *Daemon) approveMachine(user, machine string) error {
	m, ok := d.st.GetMachine(user, machine)
	if !ok {
		return nil
	}
	if m.VPNIP == "" {
		ip, err := d.st.AllocateIP(d.cfg.Subnet)
		if err != nil {
			return err
		}
		if err := d.st.SetMachineIP(user, machine, ip); err != nil {
			return err
		}
		logx.Info("allocated %s for %s/%s", ip, user, machine)
	}
	if d.srv != nil {
		d.srv.NotifyApprove(user, machine)
	}
	return nil
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
