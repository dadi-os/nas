//go:build windows

package browser

import "os/exec"

// ownProcessGroup does nothing on Windows, where the tests only run against a lone Chromium.
func ownProcessGroup(cmd *exec.Cmd) {}

// killTree kills cmd.
func killTree(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
