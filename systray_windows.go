//go:build windows

package main

import (
	"encoding/json"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
)

//go:embed nz.ico
var trayIcon []byte

var (
	dashboardPort = 19999
	serverAddr    string
	httpListener  net.Listener
	serviceRunning bool
)

type systrayState struct {
	cfg         *config
	statusLabel *systray.MenuItem
	autoStartMI *systray.MenuItem
	dashboardMI *systray.MenuItem
	quitMI      *systray.MenuItem
	connected   bool
}

func runSystray() {
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		logToFile("config error: %v", err)
		return
	}

	hideConsole()

	state := &systrayState{cfg: cfg}
	state.startDashboard()

	go state.runBackground()
	go state.pollStatus()

	logToFile("entering systray.Run")
	systray.Run(state.onReady, state.onExit)
	logToFile("systray.Run returned")
}

func logToFile(format string, args ...any) {
	exe, _ := os.Executable()
	logPath := filepath.Join(filepath.Dir(exe), "nz-startup.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("2006-01-02 15:04:05")+" "+format+"\n", args...)
}

func hideConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	showWindow := user32.NewProc("ShowWindow")
	showWindow.Call(hwnd, 0)
}

func fatalExit(title, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	u32 := syscall.NewLazyDLL("user32.dll")
	titlePtr, _ := syscall.UTF16PtrFromString("nz: " + title)
	textPtr, _ := syscall.UTF16PtrFromString(msg)
	u32.NewProc("MessageBoxW").Call(
		0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		0x10,
	)
	os.Exit(1)
}

func (s *systrayState) startDashboard() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.dashboardHandler)
	mux.HandleFunc("/favicon.ico", s.faviconHandler)
	mux.HandleFunc("/api/table", s.apiTableHandler)
	mux.HandleFunc("/api/status", s.apiStatusHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", dashboardPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		dashboardPort++
		listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", dashboardPort))
		if err != nil {
			return
		}
	}

	httpListener = listener
	serverAddr = fmt.Sprintf("http://127.0.0.1:%d", dashboardPort)

	go func() {
		http.Serve(listener, mux)
	}()
}

func (s *systrayState) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>nz ls</title>
<link rel="icon" href="/favicon.ico">
<style>
body{background:#1e1e1e;color:#ddd;font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0}
#table{font-size:14px}
table{border-collapse:collapse}
th,td{padding:8px 20px;text-align:center}
th{color:#8aadf4;border-bottom:1px solid #444}
td{border-bottom:1px solid #333}
.online{color:#a6da95}.offline{color:#ed8796}.idle{color:#eed49f}.probing{color:#eed49f}.connecting{color:#eed49f}
</style></head>
<body>
<div id="table">loading...</div>
<script>
async function load(){let r=await fetch('/api/table');let d=await r.json();let h='<table><tr><th>Name</th><th>IP</th><th>Status</th></tr>';d.forEach(p=>{h+='<tr><td>'+p.name+'</td><td>'+p.ip+'</td><td class='+p.status+'>'+p.status+'</td></tr>'});h+='</table>';document.getElementById('table').innerHTML=h}
load();setInterval(load,5000)
</script></body></html>`)
}

func (s *systrayState) faviconHandler(w http.ResponseWriter, r *http.Request) {
	if len(trayIcon) > 0 {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(trayIcon)
	}
}

func (s *systrayState) apiTableHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	list := s.queryPeerList()
	json.NewEncoder(w).Encode(list)
}

type webPeerEntry struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Status string `json:"status"`
}

func (s *systrayState) queryPeerList() []webPeerEntry {
	var list []webPeerEntry

	if s.cfg.Mode == "server" {
		list = append(list, webPeerEntry{Name: s.cfg.Name, IP: "192.168.100.1", Status: "online"})
		state, err := loadServerState(s.cfg.Password, defaultConfigPath())
		if err == nil {
			for _, n := range state.Nodes {
				status := "offline"
				ip, _ := netip.ParseAddr(n.VPNIP)
				if serviceRunning {
					status = "idle"
				}
				list = append(list, webPeerEntry{Name: n.Name, IP: ip.String(), Status: status})
			}
		}
		return list
	}

	key := sharedKey(s.cfg.Password)
	serverAddr, _ := resolveAddr(s.cfg.Domain, s.cfg.Port)
	addr := net.UDPAddrFromAddrPort(serverAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return list
	}
	defer conn.Close()

	nonce, _ := genNonce()
	encPayload, _ := encrypt(key, nonce)
	conn.Write(marshalMessage(message{Type: msgPeerList, Payload: encPayload}))

	buf := make([]byte, 65536)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	nread, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		return list
	}

	listReply, _ := unmarshalMessage(buf[:nread])
	if listReply.Type != msgPeerListRpy {
		return list
	}

	decData, err := decrypt(key, listReply.Payload)
	if err != nil {
		return list
	}

	json.Unmarshal(decData, &list)
	return list
}

func (s *systrayState) apiStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "offline"
	if s.connected {
		status = "online"
	}
	fmt.Fprintf(w, `{"status":"%s","mode":"%s","name":"%s"}`, status, s.cfg.Mode, s.cfg.Name)
}

func (s *systrayState) runBackground() {
	serviceRunning = true
	if s.cfg.Mode == "server" {
		runServer(s.cfg.Password, s.cfg.Port, s.cfg.TUN, s.cfg.Name, defaultConfigPath())
	} else {
		addr, _ := resolveAddr(s.cfg.Domain, s.cfg.Port)
		runNode(addr, s.cfg.Name, s.cfg.Password, s.cfg.TUN, s.cfg.Route, s.cfg.DesiredIP)
	}
	serviceRunning = false
}

func (s *systrayState) pollStatus() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.connected = serviceRunning
		s.refreshMenu()
	}
}

func (s *systrayState) onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("nz")
	systray.SetTooltip("nz overlay network")

	s.buildMenu()
	s.refreshMenu()
}

func (s *systrayState) buildMenu() {
	s.statusLabel = systray.AddMenuItem("nz", "Connection status")
	s.statusLabel.Disable()
	systray.AddSeparator()

	s.dashboardMI = systray.AddMenuItem("Dashboard", "Open web dashboard")
	s.autoStartMI = systray.AddMenuItem("Auto Startup", "Toggle auto-start on boot")
	systray.AddSeparator()
	s.quitMI = systray.AddMenuItem("Quit", "Exit nz")

	go func() {
		for range s.dashboardMI.ClickedCh {
			if serverAddr != "" {
				exec.Command("cmd", "/c", "start", serverAddr).Start()
			}
		}
	}()

	go func() {
		for range s.autoStartMI.ClickedCh {
			enabled, _ := isAutoStartEnabled()
			if err := setAutoStart(!enabled); err == nil {
				s.refreshMenu()
			}
		}
	}()

	go func() {
		for range s.quitMI.ClickedCh {
			systray.Quit()
		}
	}()
}

func (s *systrayState) refreshMenu() {
	if s.connected {
		s.statusLabel.SetTitle("● Online")
	} else {
		s.statusLabel.SetTitle("○ Offline")
	}

	enabled, _ := isAutoStartEnabled()
	if enabled {
		s.autoStartMI.SetTitle("✓ Auto Startup")
	} else {
		s.autoStartMI.SetTitle("Auto Startup")
	}
}

func (s *systrayState) onExit() {
	if httpListener != nil {
		httpListener.Close()
	}
}

func startupShortcutPath() (string, error) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = os.Getenv("ALLUSERSPROFILE")
	}
	if programData == "" {
		return "", fmt.Errorf("ProgramData not set")
	}
	return filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "nz.lnk"), nil
}

func setAutoStart(enabled bool) error {
	taskName := "nz"
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	if enabled {
		tr := fmt.Sprintf(`"%s" --autostart`, exePath)
		cmd := exec.Command("schtasks", "/Create", "/TN", taskName,
			"/TR", tr, "/SC", "ONLOGON", "/RL", "HIGHEST", "/F")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks create failed: %v (output: %s)", err, string(out))
		}
	} else {
		cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
		cmd.Run()
	}
	return nil
}

func isAutoStartEnabled() (bool, error) {
	cmd := exec.Command("schtasks", "/Query", "/TN", "nz")
	err := cmd.Run()
	return err == nil, nil
}
