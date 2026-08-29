// Package procscan inspects /proc to answer two questions the read-only
// commands need without the daemon: whether a pid is still alive, and
// whether a pid still belongs to "our" ffmpeg (so a recycled pid is not
// mistaken for a running re-broadcaster). It is Linux-oriented; on other
// OSes the lookups simply fail and callers treat the process as unknown.
package procscan

import (
	"fmt"
	"os"
	"strings"
)

// Alive reports whether a process with the given pid exists right now.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// CommandLine returns the process command line with arguments joined by
// spaces. It returns "" when the process does not exist or its cmdline is
// unreadable (for example a kernel thread, or a process owned by another
// user that we cannot read).
func CommandLine(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	// /proc/<pid>/cmdline is NUL-separated.
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
}

// Matches reports whether the process with pid is alive and its command
// line contains marker. Callers pass a distinctive fragment of the ffmpeg
// command line (the relay input URL) to confirm a pid still points at one
// of our re-broadcasters.
func Matches(pid int, marker string) bool {
	if !Alive(pid) || marker == "" {
		return false
	}
	return strings.Contains(CommandLine(pid), marker)
}
