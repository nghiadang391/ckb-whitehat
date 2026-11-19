//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts the agent in its own process group so the stall/timeout
// path can signal the whole group.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcess terminates the agent's whole process group, not just the
// top-level process, so a permission prompt or a stuck child cannot outlive
// the stall/timeout kill.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative pid signals the process group; fall back to the direct
	// process if the group kill is not permitted.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
