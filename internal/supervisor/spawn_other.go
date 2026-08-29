//go:build !windows

package supervisor

import (
	"os/exec"
	"syscall"
)

// configureChild puts each child in its own process group so the supervisor
// can kill the whole tree (ffmpeg plus any children) as a unit, and so a
// terminal Ctrl-C targets the daemon and not the children directly.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcess kills the child's entire process group.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// Group kill failed (e.g. the child already exited); try the
		// process directly.
		_ = cmd.Process.Kill()
	}
}
