//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// StartDetached launches cmd as an independent background process.
func StartDetached(cmd *exec.Cmd) error {
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
