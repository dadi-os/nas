//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in its own process group so killTree reaches Chromium's child processes.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills cmd and every process it started, so none still writes to the profile folder afterwards.
func killTree(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
