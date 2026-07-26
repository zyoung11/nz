package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
)

const defaultPort = 4242

func main() {
	if detectWindowsService() {
		return
	}

	for _, a := range os.Args {
		if a == "-h" || a == "--help" {
			printUsage()
			os.Exit(0)
		}
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "install":
		installCmd()
	case "uninstall":
		uninstallService()
	case "status":
		serviceStatus()
	case "run":
		runCmd()
	default:
		runLegacy()
	}
}

func installCmd() {
	configPath := flag.String("config", defaultConfigPath(), "config file path")
	flag.CommandLine.Parse(os.Args[2:])

	if _, err := loadConfig(*configPath); err != nil {
		logError("invalid config: %v", err)
		logInfo("create a config file, example:")
		logInfo(`{"mode":"server","password":"123"}`)
		logInfo(`{"mode":"node","password":"123","name":"laptop","domain":"1.2.3.4"}`)
		os.Exit(1)
	}

	if err := installService(); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

func runCmd() {
	configPath := flag.String("config", defaultConfigPath(), "config file path")
	flag.CommandLine.Parse(os.Args[2:])

	cfg, err := loadConfig(*configPath)
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	if cfg.Mode == "server" {
		if err := runServer(cfg.Password, cfg.Port, cfg.TUN, cfg.Name); err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		return
	}

	serverAddr, err := resolveAddr(cfg.Domain, cfg.Port)
	if err != nil {
		logError("failed to resolve address: %v", err)
		os.Exit(1)
	}
	if err := runNode(serverAddr, cfg.Name, cfg.Password, cfg.TUN, cfg.Route, cfg.DesiredIP); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

func runLegacy() {
	serverMode := flag.Bool("server", false, "run in server mode")
	nodeMode := flag.Bool("node", false, "run in node mode")
	configPath := flag.String("config", "", "config file path")
	password := flag.String("password", "", "shared password (required)")
	name := flag.String("name", "", "device name")
	domain := flag.String("domain", "", "server address or hostname")
	routeMode := flag.String("mode", "auto", "connection mode: auto, p2p, relay")
	tunName := flag.String("tun", "nz0", "TUNdevice name")
	port := flag.Int("port", defaultPort, "UDP端口")

	flag.CommandLine.Parse(os.Args[1:])

	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		if cfg.Mode == "server" {
			*serverMode = true
			*password = cfg.Password
			*port = cfg.Port
			*tunName = cfg.TUN
		} else {
			*nodeMode = true
			*password = cfg.Password
			*name = cfg.Name
			*domain = cfg.Domain
			*port = cfg.Port
			*tunName = cfg.TUN
			*routeMode = cfg.Route
		}
	}

	if *password == "" {
		logError("password required (--password)")
		os.Exit(1)
	}

	if *serverMode {
		if err := runServer(*password, *port, *tunName, ""); err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		return
	}

	if *nodeMode {
		if *domain == "" {
			logError("domain required for node mode (--domain)")
			os.Exit(1)
		}
		if *name == "" {
			logError("节点模式必须提供device name（--name）")
			os.Exit(1)
		}

		serverAddr, err := resolveAddr(*domain, *port)
		if err != nil {
			logError("failed to resolve address: %v", err)
			os.Exit(1)
		}

		if *routeMode != "auto" && *routeMode != "p2p" && *routeMode != "relay" {
			logError("--mode must be auto, p2p, or relay")
			os.Exit(1)
		}

		if err := runNode(serverAddr, *name, *password, *tunName, *routeMode, 0); err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		return
	}

	logError("specify --server, --node, or --config")
	os.Exit(1)
}

func resolveAddr(host string, port int) (netip.AddrPort, error) {
	ip, err := netip.ParseAddr(host)
	if err == nil {
		return netip.AddrPortFrom(ip, uint16(port)), nil
	}

	addrs, err := net.DefaultResolver.LookupNetIP(context.TODO(), "ip4", host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("failed to resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return netip.AddrPort{}, fmt.Errorf("failed to resolve %s", host)
	}
	return netip.AddrPortFrom(addrs[0].Unmap(), uint16(port)), nil
}

func printUsage() {
	cMauve := "\x1b[38;2;198;160;246m"
	cBold := "\x1b[1m"
	cGreen := "\x1b[38;2;166;218;149m"
	cYellow := "\x1b[38;2;238;212;159m"
	cReset := "\x1b[0m"

	app := colorBlue + "netzero" + cReset
	portStr := strconv.Itoa(defaultPort)

	fmt.Printf(
		"\n"+`%s%sNAME:%s
  %s - overlay network tool

%s%sUSAGE:%s
  %s --server --password %s<password>%s
  %s --node --name %s<name>%s --domain %s<addr>%s --password %s<password>%s
  %s --config %s<path>%s

%s%sCOMMANDS:%s
  %s install  %s--config %s<path>%s  Register as system service (auto-start on boot)
  %s uninstall %s                Stop and remove service
  %s status   %s                 Show service status

%s%sOPTIONS:%s
  %s--password%s  Shared password
  %s--name%s      Device name (node mode)
  %s--domain%s    Server address or hostname (node mode)
  %s--mode%s      Connection mode: %sauto%s (default), %sp2p%s, %srelay%s
  %s--ip%s        Request specific VPN IP (2-254)
  %s--port%s      UDP port (default: %s%s%s)
  %s--config%s    Config file path (default: ./config.json)
  %s--tun%s       TUN device name (default: nz0)

%s%sCONFIG FILE:%s
  %s{"mode":"server","password":"123","name":"Server"}%s
  %s{"mode":"node","password":"123","name":"laptop","domain":"my-server.com","ip":5}%s

%s%sNETWORK:%s
  192.168.100.0/24 — server fixed at 192.168.100.1, nodes allocated .2 through .254
  Optional "ip" in config to request a specific address
`,
		cBold, cMauve, cReset,
		app,
		cBold, cMauve, cReset,
		app, cYellow, cReset,
		app, cYellow, cReset, cYellow, cReset, cYellow, cReset,
		app, cYellow, cReset,
		cBold, cMauve, cReset,
		cGreen, cYellow, cYellow, cReset,
		cGreen, cReset,
		cGreen, cReset,
		cBold, cMauve, cReset,
		cYellow, cReset,
		cYellow, cReset,
		cYellow, cReset,
		cYellow, cReset, cGreen, cReset, cGreen, cReset, cGreen, cReset,
		cYellow, cReset,
		cYellow, cReset, cYellow, portStr, cReset,
		cYellow, cReset,
		cYellow, cReset,
		cBold, cMauve, cReset,
		cGreen, cReset,
		cGreen, cReset,
		cBold, cMauve, cReset,
	)
}
