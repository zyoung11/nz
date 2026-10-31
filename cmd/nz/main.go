package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nz/internal/config"
	"nz/internal/daemon"
	"nz/internal/ipc"
	"nz/internal/logx"
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

var commands = []command{
	{
		name: "run", summary: "Run in foreground for debugging",
		usage: "nz run [--config <path>] [--log]",
		options: [][2]string{
			{"--config <path>", "config file path"},
			{"--log", "write nz.log next to the binary"},
		},
		run: runCmd,
	},
	{
		name: "install", summary: "Start the daemon and enable auto-start",
		usage: "nz install [--config <path>]",
		options: [][2]string{
			{"--config <path>", "config file path"},
		},
		run: installCmd,
	},
	{
		name: "uninstall", summary: "Stop the daemon and disable auto-start",
		usage: "nz uninstall",
		run:   uninstallCmd,
	},
	{
		name: "reload", summary: "Validate config and apply it",
		usage: "nz reload",
		run:   reloadCmd,
	},
	{
		name: "list", summary: "Show status and the machine table",
		usage: "nz list [--pending]",
		options: [][2]string{
			{"--pending", "show join requests only"},
		},
		run: listCmd,
	},
	{
		name: "ls", summary: "Short form of list",
		usage: "nz ls [--pending]",
		options: [][2]string{
			{"--pending", "show join requests only"},
		},
		run: listCmd,
	},
	{
		name: "allow", summary: "Approve or unblock or authorize",
		usage: "nz allow <target> [--to <machine>]",
		options: [][2]string{
			{"--to <machine>", "machine to authorize, default this machine"},
		},
		run: grantCmd("allow", "Approve or unblock or authorize"),
	},
	{
		name: "deny", summary: "Reject or block or revoke",
		usage: "nz deny <target> [--to <machine>]",
		options: [][2]string{
			{"--to <machine>", "machine to revoke, default this machine"},
		},
		run: grantCmd("deny", "Reject or block or revoke"),
	},
	{
		name: "version", summary: "Show version",
		usage: "nz version",
		run:   versionCmd,
	},
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	name := os.Args[1]
	switch name {
	case "-v", "-V", "--version":
		name = "version"
	case "-h", "--help", "help":
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

func isHelpArg(s string) bool {
	return s == "-h" || s == "--help" || s == "help"
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "run", summary: "Run in foreground for debugging",
			usage: "nz run [--config <path>] [--log]",
			options: [][2]string{
				{"--config <path>", "config file path"},
				{"--log", "write nz.log next to the binary"},
			},
		})
	}
	cfgPath := fs.String("config", config.DefaultPath(), "config file path")
	logFlag := fs.Bool("log", false, "write nz.log next to the binary")
	pingback := fs.String("pingback", "", "pingback address for background start")
	token := fs.String("token", "", "pingback token for background start")
	fs.Parse(args)

	if daemon.CheckSocket() {
		logx.Error("daemon is already running")
		logx.Info("use nz uninstall first")
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

func logFilePath() string {
	exe, err := os.Executable()
	if err != nil {
		return config.LogFileName
	}
	return filepath.Join(filepath.Dir(exe), config.LogFileName)
}

func installCmd(args []string) int {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "install", summary: "Start the daemon and enable auto-start",
			usage: "nz install [--config <path>]",
			options: [][2]string{
				{"--config <path>", "config file path"},
			},
		})
	}
	cfgPath := fs.String("config", config.DefaultPath(), "config file path")
	fs.Parse(args)
	if err := config.ValidateFile(*cfgPath); err != nil {
		logx.Error("invalid config: %v", err)
		return 1
	}
	if err := service.Install(*cfgPath); err != nil {
		logx.Error("%v", err)
		return 1
	}
	logx.Success("daemon started and auto-start enabled")
	return 0
}

func uninstallCmd(_ []string) int {
	if err := service.Uninstall(); err != nil {
		logx.Error("%v", err)
		return 1
	}
	logx.Success("daemon stopped and auto-start disabled")
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

type statusView struct {
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

func listCmd(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Usage = func() {
		printCommandUsage(command{
			name: "list", summary: "Show status and the machine table",
			usage: "nz list [--pending]",
			options: [][2]string{
				{"--pending", "show join requests only"},
			},
		})
	}
	pending := fs.Bool("pending", false, "show join requests only")
	fs.Parse(args)

	resp, ok := send(ipc.Request{Command: "list"})
	if !ok {
		return 1
	}
	if !resp.OK {
		logx.Error("%s", resp.Error)
		return 1
	}
	var data struct {
		Status   statusView          `json:"status"`
		Users    []state.User        `json:"users"`
		Machines []state.MachineView `json:"machines"`
		Grants   []state.Grant       `json:"grants"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &data); err != nil {
		logx.Error("failed to decode list: %v", err)
		return 1
	}
	printStatus(data.Status)
	printUserTable(data.Users, data.Machines, data.Grants, *pending)
	return 0
}

func printStatus(st statusView) {
	logx.Info("running  version %s  mode %s  port %d  subnet %s",
		st.Version, st.Mode, st.Port, st.Subnet)
	if st.Mode == "node" {
		ip := st.VPNIP
		if ip == "" {
			ip = "not assigned"
		}
		logx.Info("connection %s  vpn ip %s", st.Conn, ip)
		logx.Info("uptime %s", formatDuration(st.Uptime))
	} else {
		logx.Info("uptime %s  users %d  machines %d  pending %d",
			formatDuration(st.Uptime), st.Users, st.Machines, st.Pending)
	}
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

func grantCmd(name, summary string) func([]string) int {
	return func(args []string) int {
		subject, to, ok := parseTargetArgs(args)
		if !ok {
			logx.Error("nz %s needs one target", name)
			printCommandUsage(command{name: name, summary: summary, usage: "nz " + name + " <target> [--to <machine>]"})
			return 1
		}
		resp, ok := send(ipc.Request{Command: name, Target: subject, To: to})
		if !ok {
			return 1
		}
		if !resp.OK {
			logx.Error("%s", resp.Error)
			return 1
		}
		logx.Success("%s %s done", name, subject)
		return 0
	}
}

// parseTargetArgs accepts one target and an optional --to machine in
// any order.
func parseTargetArgs(args []string) (string, string, bool) {
	subject := ""
	to := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--to" && i+1 < len(args):
			to = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--to="):
			to = strings.TrimPrefix(args[i], "--to=")
		case isHelpArg(args[i]) || strings.HasPrefix(args[i], "-"):
			return "", "", false
		default:
			if subject != "" {
				return "", "", false
			}
			subject = args[i]
		}
	}
	if subject == "" {
		return "", "", false
	}
	return subject, to, true
}

func send(req ipc.Request) (ipc.Response, bool) {
	resp, err := ipc.CommandRequest(ipc.SocketPath(), req)
	if err != nil {
		if _, statErr := os.Stat(ipc.SocketPath()); statErr == nil {
			logx.Error("admin socket exists but no daemon answers")
			logx.Info("start it with nz install")
		} else {
			logx.Error("daemon is not running")
			logx.Info("start it with nz install")
		}
		return resp, false
	}
	return resp, true
}

func versionCmd(_ []string) int {
	fmt.Printf("%snz%s %s%s%s\n", colorBlue, colorReset, colorGreen, version, colorReset)
	return 0
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
