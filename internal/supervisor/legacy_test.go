package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xlip/multistream/internal/procscan"
)

// deadChildPid starts and waits a child so its pid is guaranteed dead.
func deadChildPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return pid
}

// waitForCmdline blocks until /proc/<pid>/cmdline contains marker (right
// after an exec the kernel may not have published the command line yet).
func waitForCmdline(t *testing.T, pid int, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if procscan.Matches(pid, marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d never showed a command line containing %q", pid, marker)
}

func TestLegacyDaemonPIDAbsent(t *testing.T) {
	// Missing root: nothing to check.
	if pid, running := LegacyDaemonPID(filepath.Join(t.TempDir(), "nope")); running || pid != 0 {
		t.Errorf("missing root = (%d, %v), want (0, false)", pid, running)
	}
	// Root without a daemon.pid.
	if pid, running := LegacyDaemonPID(t.TempDir()); running || pid != 0 {
		t.Errorf("no pid file = (%d, %v), want (0, false)", pid, running)
	}
	// Unparseable pid file.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "daemon.pid"), []byte("not-a-pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pid, running := LegacyDaemonPID(root); running || pid != 0 {
		t.Errorf("garbage pid file = (%d, %v), want (0, false)", pid, running)
	}
	// Pid file pointing at a dead process.
	if err := os.WriteFile(filepath.Join(root, "daemon.pid"), []byte(strconv.Itoa(deadChildPid(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	if pid, running := LegacyDaemonPID(root); running || pid != 0 {
		t.Errorf("dead pid = (%d, %v), want (0, false)", pid, running)
	}
}

func TestLegacyDaemonPIDLive(t *testing.T) {
	skipIfNotLinux(t)
	// A process whose command line contains "multistream", standing in for
	// a pre-profile daemon.
	dir := t.TempDir()
	script := filepath.Join(dir, "multistream")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	go func() { _ = cmd.Wait() }() // reap: a killed child must leave /proc
	pid := cmd.Process.Pid
	waitForCmdline(t, pid, "multistream")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "daemon.pid"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, running := LegacyDaemonPID(root)
	if !running || got != pid {
		t.Errorf("LegacyDaemonPID = (%d, %v), want (%d, true)", got, running, pid)
	}
}

func TestLegacyDaemonPIDLiveButUnrelated(t *testing.T) {
	skipIfNotLinux(t)
	// A recycled pid that belongs to a process without "multistream" in its
	// command line must not be mistaken for the legacy daemon.
	dir := t.TempDir()
	script := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	waitForCmdline(t, pid, "unrelated")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "daemon.pid"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, running := LegacyDaemonPID(root); running || got != 0 {
		t.Errorf("unrelated pid = (%d, %v), want (0, false)", got, running)
	}
}
