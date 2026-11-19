//go:build windows

package agent

import "os/exec"

// setProcAttr is a no-op on Windows: process groups are a Unix concept. The
// agent still runs as a child, so the stall/timeout kill below still applies.
func setProcAttr(cmd *exec.Cmd) {}

// killProcess terminates the agent process on Windows. The Unix process-group
// kill is not portable; a direct kill is all this platform offers.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
