package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
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

	switch os.Args[1] {
	case "install":
		installCmd()
	case "uninstall":
		uninstallService()
	case "ls":
		lsCmd()
	case "run":
		runCmd()
	case "help":
		printUsage()
	default:
		printUsage()
		os.Exit(1)
	}
}

func installCmd() {
	var configPath string
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	fs.StringVar(&configPath, "config", defaultConfigPath(), "config file path")
	fs.Parse(os.Args[2:])

	if _, err := loadConfig(configPath); err != nil {
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
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	if cfg.Mode == "server" {
		if err := runServer(cfg.Password, cfg.Port, cfg.TUN, cfg.Name, defaultConfigPath()); err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		return
	}

	serverAddr, err := resolveAddr(cfg.Domain, cfg.Port)
	if err != nil {
		logError("failed to resolve server: %v", err)
		os.Exit(1)
	}
	if err := runNode(serverAddr, cfg.Name, cfg.Password, cfg.TUN, cfg.Route, cfg.DesiredIP); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

func lsCmd() {
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		logError("%v", err)
		os.Exit(1)
	}

	key := sharedKey(cfg.Password)

	var serverAddr netip.AddrPort
	if cfg.Mode == "server" {
		serverAddr = netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(cfg.Port))
	} else {
		var err error
		serverAddr, err = resolveAddr(cfg.Domain, cfg.Port)
		if err != nil {
			logError("failed to resolve server: %v", err)
			os.Exit(1)
		}
	}

	addr := net.UDPAddrFromAddrPort(serverAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		logError("failed to connect: %v", err)
		os.Exit(1)
	}
	defer conn.Close()

	nonce, _ := genNonce()
	encPayload, _ := encrypt(key, nonce)
	listMsg := marshalMessage(message{Type: msgPeerList, Payload: encPayload})
	conn.Write(listMsg)

	buf := make([]byte, 65536)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	nread, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		logError("no response: %v", err)
		os.Exit(1)
	}

	listReply, _ := unmarshalMessage(buf[:nread])
	if listReply.Type != msgPeerListRpy {
		logError("unexpected reply (type %02x)", listReply.Type)
		os.Exit(1)
	}

	decData, err := decrypt(key, listReply.Payload)
	if err != nil {
		logError("decrypt failed: %v", err)
		os.Exit(1)
	}

	type peerEntry struct {
		Name   string `json:"name"`
		IP     string `json:"ip"`
		Status string `json:"status"`
	}
	var list []peerEntry
	json.Unmarshal(decData, &list)

	localName := cfg.Name

	nameWidth, ipWidth, statusWidth := 4, 2, 6
	for _, e := range list {
		if len(e.Name) > nameWidth {
			nameWidth = len(e.Name)
		}
		if len(e.IP) > ipWidth {
			ipWidth = len(e.IP)
		}
		if len(e.Status) > statusWidth {
			statusWidth = len(e.Status)
		}
	}
	nameWidth += 2
	ipWidth += 2
	statusWidth += 2

	top := "┌" + repStr("─", nameWidth) + "┬" + repStr("─", ipWidth) + "┬" + repStr("─", statusWidth) + "┐"
	header := "│" + padStr("Name", nameWidth) + "│" + padStr("IP", ipWidth) + "│" + padStr("Status", statusWidth) + "│"
	sep := "├" + repStr("─", nameWidth) + "┼" + repStr("─", ipWidth) + "┼" + repStr("─", statusWidth) + "┤"
	bottom := "└" + repStr("─", nameWidth) + "┴" + repStr("─", ipWidth) + "┴" + repStr("─", statusWidth) + "┘"

	fmt.Println(top)
	fmt.Println(header)
	fmt.Println(sep)
	for i, e := range list {
		isLocal := i > 0 && e.Name == localName
		if cfg.Mode == "server" {
			isLocal = i == 0
		}
		rowColor := colorReset
		if isLocal {
			rowColor = colorBlue
		}

		statusColor := colorGreen
		switch e.Status {
		case "offline":
			statusColor = colorRed
		case "probing", "connecting", "idle":
			statusColor = colorYellow
		}

		row := "│" + rowColor + padStr(e.Name, nameWidth) + colorReset + "│" + rowColor + padStr(e.IP, ipWidth) + colorReset + "│" + statusColor + padStr(e.Status, statusWidth) + colorReset + "│"
		fmt.Println(row)
	}
	fmt.Println(bottom)
}

func repStr(s string, n int) string {
	return strings.Repeat(s, n)
}

func padStr(s string, w int) string {
	if len(s) >= w {
		return " " + s + " "
	}
	left := (w - len(s)) / 2
	right := w - len(s) - left
	return repStr(" ", left) + s + repStr(" ", right)
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

	app := colorBlue + "nz" + cReset

	fmt.Printf(
		"\n"+`%s%sNAME:%s
  %s - overlay network tool

%s%sUSAGE:%s
  Create %sconfig.json%s next to the binary, then:

  %s install %s--config %s<path>%s  Register as system service
  %s uninstall %s               Stop and remove service
  %s ls       %s                List all nodes
  %s run      %s                Run in foreground

%s%sCONFIG:%s
  Server: %s{"mode":"server","password":"123"}%s
  Node:   %s{"mode":"node","password":"123","name":"laptop","domain":"my-server.com"}%s

%s%sNETWORK:%s
  192.168.100.0/24 — server fixed at .1, nodes allocated .2 through .254
`,
		cBold, cMauve, cReset,
		app,
		cBold, cMauve, cReset,
		cYellow, cReset,
		cGreen, cYellow, cYellow, cReset,
		cGreen, cReset,
		cGreen, cReset,
		cGreen, cReset,
		cBold, cMauve, cReset,
		cGreen, cReset,
		cGreen, cReset,
		cBold, cMauve, cReset,
	)
}
