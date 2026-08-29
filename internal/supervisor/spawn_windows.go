//go:build windows

package supervisor

import (
	"os/exec"
	"syscall"
)

// configureChild keeps the ffmpeg child from popping a console window when
// the daemon runs in a Windows console.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// killProcess kills the child. (Windows lacks POSIX process groups; a job
// object would be the fuller solution but is unnecessary since ffmpeg is a
// single process.)
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
