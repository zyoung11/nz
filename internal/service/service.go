package service

import "path/filepath"

// Install registers the daemon for auto-start on boot.
func Install(cfgPath string) error {
	abs, err := filepath.Abs(cfgPath)
	if err != nil {
		return err
	}
	return install(abs)
}

// Uninstall removes the auto-start registration.
func Uninstall() error {
	return uninstall()
}

// RunIfService runs the daemon body directly, or inside the Windows
// service control manager when started as a service. The stop hook
// terminates the daemon body on a service stop request.
func RunIfService(run func() error, stop func()) error {
	return runIfService(run, stop)
}
