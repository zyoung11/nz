//go:build windows

package service

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "n0"

func install(cfgPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binPath := fmt.Sprintf(`"%s" run --config "%s"`, exe, cfgPath)
	if out, err := exec.Command("sc", "create", serviceName,
		"binPath=", binPath, "start=", "auto").CombinedOutput(); err != nil {
		return fmt.Errorf("sc create failed: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("sc", "failure", serviceName,
		"reset=", "86400",
		"actions=", "restart/5000/restart/10000/restart/30000").CombinedOutput(); err != nil {
		return fmt.Errorf("sc failure failed: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("sc", "start", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("sc start failed: %v (%s)", err, string(out))
	}
	return nil
}

func uninstall() error {
	exec.Command("sc", "stop", serviceName).Run()
	out, err := exec.Command("sc", "delete", serviceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc delete failed: %v (%s)", err, string(out))
	}
	return nil
}

func runIfService(run func() error, stop func()) error {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return run()
	}
	return svc.Run(serviceName, &serviceRunner{run: run, stop: stop})
}

type serviceRunner struct {
	run  func() error
	stop func()
}

func (r *serviceRunner) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- r.run() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				r.stop()
				return false, 0
			}
		}
	}
}
