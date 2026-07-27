//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

//go:embed wintun.dll
var wintunDLL []byte

func initWintun() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	dllPath := filepath.Join(filepath.Dir(exePath), "wintun.dll")
	if _, err := os.Stat(dllPath); err == nil {
		return nil
	}
	return os.WriteFile(dllPath, wintunDLL, 0644)
}

func detectWindowsService() bool {
	isService, err := isWindowsService()
	if err != nil || !isService {
		return false
	}
	if execPath, err := os.Executable(); err == nil {
		os.Chdir(execPath)
	}
	if err := runAsService(); err != nil {
		os.Exit(1)
	}
	return true
}

const serviceName = "nz"

func isWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

func runAsService() error {
	return svc.Run(serviceName, &windowsService{})
}

type windowsService struct{}

func (ws *windowsService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	cfgPath, err := serviceConfigPath()
	if err != nil {
		return false, 1
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return false, 1
	}

	errCh := make(chan error, 1)
	if cfg.Mode == "server" {
		go func() { errCh <- runServer(cfg.Password, cfg.Port, cfg.TUN, cfg.Name, defaultConfigPath()) }()
	} else {
		go func() {
			addr, e := resolveAddr(cfg.Domain, cfg.Port)
			if e != nil {
				errCh <- e
				return
			}
			errCh <- runNode(addr, cfg.Name, cfg.Password, cfg.TUN, cfg.Route, cfg.DesiredIP)
		}()
	}

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case r := <-req:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		case err := <-errCh:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}

func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	cfgPath, err := serviceConfigPath()
	if err != nil {
		return err
	}

	absExe, _ := filepath.Abs(exe)
	absCfg, _ := filepath.Abs(cfgPath)
	binPath := fmt.Sprintf(`"%s" run --config "%s"`, absExe, absCfg)

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.CreateService(serviceName, absExe, mgr.Config{
		DisplayName: "nz",
		Description: "nz overlay network service",
		StartType:   mgr.StartAutomatic,
	}, binPath)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			s, err = m.OpenService(serviceName)
			if err != nil {
				return fmt.Errorf("failed to open existing service: %w", err)
			}
			defer s.Close()
			if err := s.UpdateConfig(mgr.Config{StartType: mgr.StartAutomatic}); err != nil {
				return fmt.Errorf("failed to update service config: %w", err)
			}
			logWarn("service exists, config updated")
		} else {
			return fmt.Errorf("failed to create service: %w", err)
		}
	} else {
		defer s.Close()
	}

	if err := s.Start(); err != nil && !strings.Contains(err.Error(), "already running") {
		return fmt.Errorf("failed to start service: %w", err)
	}

	logSuccess("service installed and started")
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		logWarn("cannot connect to SCM, service may not exist")
		return nil
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		logWarn("service not installed")
		return nil
	}
	s.Control(svc.Stop)
	s.Close()

	cmd := exec.Command("sc", "delete", serviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "not exist") {
			logWarn("failed to delete service: %s", out)
		}
	}

	logSuccess("service removed")
	return nil
}

func serviceStatus() error {
	m, err := mgr.Connect()
	if err != nil {
		logError("status: cannot connect to SCM")
		return nil
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		logError("status: not installed")
		return nil
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("failed to query service status: %w", err)
	}

	switch st.State {
	case svc.Running:
		logSuccess("status: running")
	case svc.Stopped:
		logError("status: stopped")
	default:
		logInfo("status: %d", st.State)
	}
	return nil
}
