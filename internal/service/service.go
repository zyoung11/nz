package service

// Install registers the daemon for auto-start on boot.
func Install(cfgPath string) error {
	return install(cfgPath)
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
