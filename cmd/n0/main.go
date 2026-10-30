package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nz/internal/config"
	"nz/internal/daemon"
	"nz/internal/ipc"
	"nz/internal/logx"
	"nz/internal/protocol"
	"nz/internal/service"
	"nz/internal/state"
	"nz/internal/tun"
)

const version = "0.3.0"

const (
	colorMauve  = "\x1b[38;2;198;160;246m"
	colorBlue   = "\x1b[38;2;138;173;244m"
	colorGreen  = "\x1b[38;2;166;218;149m"
	colorYellow = "\x1b[38;2;238;212;159m"
	colorBold   = "\x1b[1m"
	colorReset  = "\x1b[0m"
)

type command struct {
	name    string
	summary string
	usage   string
	options [][2]string
	run     func(args []string) int
}

func cmd(name, summary, usage string, run func([]string) int) command {
	return command{name: name, summary: summary, usage: usage, run: run}
}

func cmdWith(name, summary, usage string, options [][2]string, run func([]string) int) command {
	return command{name: name, summary: summary, usage: usage, options: options, run: run}
}

var commands = []command{
	cmdWith("run", "Run in foreground", "n0 run [--config <path>] [--log]",
		[][2]string{
			{"--config <path>", "config file path"},
			{"--log", "write n0.log next to the binary"},
		}, runCmd),
	cmdWith("start", "Run in background", "n0 start [--config <path>] [--log]",
		[][2]string{
			{"--config <path>", "config file path"},
			{"--log", "write n0.log next to the binary"},
		}, startCmd),
	cmd("stop", "Stop the daemon", "n0 stop", stopCmd),
	cmd("reload", "Validate and apply config", "n0 reload", reloadCmd),
	cmd("status", "Show daemon status", "n0 status", statusCmd),
	cmd("list", "List machines", "n0 list", listCmd),
	cmdWith("users", "List users and machines", "n0 users [--pending]",
		[][2]string{{"--pending", "show join requests only"}}, usersCmd),
	cmdWith("install", "Enable auto-start on boot", "n0 install [--config <path>]",
		[][2]string{{"--config <path>", "config file path"}}, installCmd),
	cmd("uninstall", "Disable auto-start on boot", "n0 uninstall", uninstallCmd),
	cmd("allow", "Approve a join request", "n0 allow <target>", adminCmd("allow", "Approve a join request")),
	cmd("deny", "Reject a join request", "n0 deny <target>", adminCmd("deny", "Reject a join request")),
	cmd("block", "Block a user or machine", "n0 block <target>", adminCmd("block", "Block a user or machine")),
	cmd("unblock", "Unblock a user or machine", "n0 unblock <target>", adminCmd("unblock", "Unblock a user or machine")),
	cmd("trust", "Trust a user or machine", "n0 trust <target>", adminCmd("trust", "Trust a user or machine")),
	cmd("untrust", "Revoke trust", "n0 untrust <target>", adminCmd("untrust", "Revoke trust")),
	cmd("version", "Show version", "n0 version", versionCmd),
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		printUsage()
		os.Exit(0)
	}
	for _, c := range commands {
		if c.name == name {
			if len(os.Args) > 2 && isHelpArg(os.Args[2]) {
				printCommandUsage(c)
				os.Exit(0)
			}
			os.Exit(c.run(os.Args[2:]))
		}
	}
	logx.Error("unknown command %s", name)
	printUsage()
	os.Exit(1)
}

func installCmd(args []string) int {
	cfgPath := configFlag("install", "Enable auto-start on boot", args)
	if err := config.ValidateFile(*cfgPath); err != nil {
		logx.Error("invalid config: %v", err)
		return 1
	}
	if err := service.Install(*cfgPath); err != nil {
		logx.Error("%v", err)
		return 1
	}
	logx.Success("auto-start enabled and started")
	return 0
}

func uninstallCmd(_ []string) int {
	if err := service.Uninstall(); err != nil {
		logx.Error("%v", err)
		return 1
	}
	logx.Success("auto-start disabled and stopped")
	return 0
}

func isHelpArg(s string) bool {
	return s == "-h" || s == "--help" || s == "help"
}

func configFlag(name, summary string, args []string) *string {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: name, summary: summary, usage: "n0 " + name + " [--config <path>]",
			options: [][2]string{{"--config <path>", "config file path"}},
		})
	}
	path := fs.String("config", config.DefaultPath(), "config file path")
	fs.Parse(args)
	return path
}

func printCommandUsage(c command) {
	fmt.Printf(
		"\n%s%sn0 %s%s — %s%s\n\n"+
			"%sUSAGE:%s\n"+
			"  %s%s%s\n",
		colorBold, colorBlue, c.name, colorReset, c.summary, colorReset,
		colorBold+colorMauve, colorReset,
		colorBlue, c.usage, colorReset,
	)
	if len(c.options) > 0 {
		fmt.Printf("\n%sOPTIONS:%s\n", colorBold+colorMauve, colorReset)
		width := 0
		for _, o := range c.options {
			if len(o[0]) > width {
				width = len(o[0])
			}
		}
		for _, o := range c.options {
			fmt.Printf("  %s%s%s %s\n", colorYellow,
				o[0]+strings.Repeat(" ", width-len(o[0])), colorReset, o[1])
		}
	}
	if strings.HasSuffix(c.usage, "<target>") {
		printTargetHint()
	}
	fmt.Println()
}

func printTargetHint() {
	fmt.Printf(
		"\n%sTARGET:%s\n"+
			"  %sfriend%s        a user\n"+
			"  %sfriend/laptop%s a machine\n",
		colorBold+colorMauve, colorReset,
		colorYellow, colorReset,
		colorYellow, colorReset,
	)
}

func logFilePath() string {
	exe, err := os.Executable()
	if err != nil {
		return config.LogFileName
	}
	return filepath.Join(filepath.Dir(exe), config.LogFileName)
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "run", summary: "Run in foreground", usage: "n0 run [--config <path>] [--log]",
			options: [][2]string{
				{"--config <path>", "config file path"},
				{"--log", "write n0.log next to the binary"},
			},
		})
	}
	cfgPath := fs.String("config", config.DefaultPath(), "config file path")
	logFlag := fs.Bool("log", false, "write n0.log next to the binary")
	pingback := fs.String("pingback", "", "pingback address for background start")
	token := fs.String("token", "", "pingback token for background start")
	fs.Parse(args)

	if daemon.CheckSocket() {
		logx.Error("daemon is already running")
		logx.Info("use n0 stop first")
		return 1
	}
	if err := tun.CheckPermission(); err != nil {
		logx.Error("%v", err)
		return 1
	}
	if *logFlag {
		if _, err := logx.OpenLogFile(logFilePath()); err != nil {
			logx.Error("%v", err)
			return 1
		}
	}
	d, err := daemon.New(*cfgPath, version)
	if err != nil {
		logx.Error("%v", err)
		return 1
	}
	if err := service.RunIfService(func() error {
		return d.Serve(*pingback, *token)
	}, d.Stop); err != nil {
		logx.Error("%v", err)
		return 1
	}
	return 0
}

func startCmd(args []string) int {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "start", summary: "Run in background", usage: "n0 start [--config <path>] [--log]",
			options: [][2]string{
				{"--config <path>", "config file path"},
				{"--log", "write n0.log next to the binary"},
			},
		})
	}
	cfgPath := fs.String("config", config.DefaultPath(), "config file path")
	logFlag := fs.Bool("log", false, "write n0.log next to the binary")
	fs.Parse(args)
	if err := config.ValidateFile(*cfgPath); err != nil {
		logx.Error("invalid config: %v", err)
		return 1
	}
	if err := tun.CheckPermission(); err != nil {
		logx.Error("%v", err)
		return 1
	}
	if daemon.CheckSocket() {
		logx.Warn("daemon is already running")
		return 1
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logx.Error("failed to open pingback listener: %v", err)
		return 1
	}
	defer ln.Close()

	token := randomToken()
	runArgs := []string{"run", "--config", *cfgPath,
		"--pingback", ln.Addr().String(), "--token", token}
	if *logFlag {
		runArgs = append(runArgs, "--log")
	}
	cmd := exec.Command(os.Args[0], runArgs...)
	if err := daemon.StartDetached(cmd); err != nil {
		logx.Error("failed to start daemon: %v", err)
		return 1
	}

	ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	conn, err := ln.Accept()
	if err != nil {
		logx.Error("daemon did not confirm startup in 10 s")
		return 1
	}
	defer conn.Close()
	if daemon.ReadPingbackToken(conn) != token {
		logx.Error("daemon startup confirmation mismatch")
		return 1
	}
	logx.Success("daemon started in background")
	return 0
}

func randomToken() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func send(req ipc.Request) (ipc.Response, bool) {
	resp, err := ipc.CommandRequest(ipc.SocketPath(), req)
	if err != nil {
		if _, statErr := os.Stat(ipc.SocketPath()); statErr == nil {
			logx.Error("daemon is running but the admin socket is not accessible")
			logx.Info("run this command with sudo")
		} else {
			logx.Error("daemon is not running")
			logx.Info("start it with n0 run or n0 start")
		}
		return resp, false
	}
	return resp, true
}

func stopCmd(_ []string) int {
	resp, ok := send(ipc.Request{Command: "stop"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	logx.Success("daemon stopped")
	return 0
}

func reloadCmd(_ []string) int {
	resp, ok := send(ipc.Request{Command: "reload"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	logx.Success("config reloaded")
	return 0
}

func statusCmd(_ []string) int {
	resp, ok := send(ipc.Request{Command: "status"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	var st struct {
		Version  string `json:"version"`
		Conn     string `json:"conn"`
		VPNIP    string `json:"vpn_ip"`
		Mode     string `json:"mode"`
		Port     int    `json:"port"`
		Subnet   string `json:"subnet"`
		Uptime   int64  `json:"uptime_sec"`
		Users    int    `json:"users"`
		Machines int    `json:"machines"`
		Pending  int    `json:"pending"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &st); err != nil {
		logx.Error("failed to decode status: %v", err)
		return 1
	}
	logx.Info("running  version %s  mode %s  port %d  subnet %s",
		st.Version, st.Mode, st.Port, st.Subnet)
	if st.Mode == "node" {
		ip := st.VPNIP
		if ip == "" {
			ip = "not assigned"
		}
		logx.Info("connection %s  vpn ip %s", st.Conn, ip)
	}
	if st.Mode == "server" {
		logx.Info("uptime %s  users %d  machines %d  pending %d",
			formatDuration(st.Uptime), st.Users, st.Machines, st.Pending)
	} else {
		logx.Info("uptime %s", formatDuration(st.Uptime))
	}
	return 0
}

func formatDuration(sec int64) string {
	d := time.Duration(sec) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%ds", sec)
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), sec%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func listCmd(_ []string) int {
	resp, ok := send(ipc.Request{Command: "list"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	var peers []protocol.PeerInfo
	if err := json.Unmarshal([]byte(resp.Data), &peers); err != nil {
		logx.Error("failed to decode machine list: %v", err)
		return 1
	}
	printMachineTable(peers)
	return 0
}

func usersCmd(args []string) int {
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "users", summary: "List users and machines", usage: "n0 users [--pending]",
			options: [][2]string{{"--pending", "show join requests only"}},
		})
	}
	pending := fs.Bool("pending", false, "show join requests only")
	fs.Parse(args)

	resp, ok := send(ipc.Request{Command: "users"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	var data struct {
		Users    []state.User    `json:"users"`
		Machines []state.Machine `json:"machines"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &data); err != nil {
		logx.Error("failed to decode user list: %v", err)
		return 1
	}
	printUserTable(data.Users, data.Machines, *pending)
	return 0
}

func adminCmd(name, summary string) func(args []string) int {
	return func(args []string) int {
		if len(args) != 1 {
			logx.Error("n0 %s needs one target", name)
			printCommandUsage(command{name: name, summary: summary, usage: "n0 " + name + " <target>"})
			return 1
		}
		resp, ok := send(ipc.Request{Command: name, Target: args[0]})
		if !ok {
			return 1
		}
		if !resp.OK {
			logx.Error("%s", resp.Error)
			return 1
		}
		logx.Success("%s %s done", name, args[0])
		return 0
	}
}

func versionCmd(_ []string) int {
	fmt.Printf("%sn0%s %s%s%s\n", colorBlue, colorReset, colorGreen, version, colorReset)
	return 0
}

func printUsage() {
	fmt.Printf(
		"\n%s%sn0%s — overlay network tool\n\n"+
			"%sUSAGE:%s\n"+
			"  %sn0%s <command> %s--config <path>%s\n\n"+
			"%sCOMMANDS:%s\n",
		colorBold, colorBlue, colorReset,
		colorBold+colorMauve, colorReset,
		colorBlue, colorReset, colorYellow, colorReset,
		colorBold+colorMauve, colorReset,
	)
	for _, c := range commands {
		fmt.Printf("  %s%-10s%s %s\n", colorGreen, c.name, colorReset, c.summary)
	}
	printTargetHint()
	fmt.Println()
}
