package procscan

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// skipIfNotLinux skips on non-Linux platforms, where the /proc lookups this
// package is built on do not exist (callers gate their usage accordingly).
func skipIfNotLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("procscan is /proc-based; Linux only")
	}
}

func TestAlive(t *testing.T) {
	skipIfNotLinux(t)
	if !Alive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if Alive(0) {
		t.Error("pid 0 should not be alive")
	}
	if Alive(-1) {
		t.Error("negative pid should not be alive")
	}
	if Alive(99999999) {
		t.Error("nonexistent pid should not be alive")
	}
}

func TestCommandLine(t *testing.T) {
	skipIfNotLinux(t)
	cl := CommandLine(os.Getpid())
	if cl == "" {
		t.Fatal("own command line should not be empty")
	}
	// The test binary's path contains the package name.
	if !strings.Contains(cl, "procscan") {
		t.Errorf("own command line %q should contain %q", cl, "procscan")
	}
	if CommandLine(0) != "" {
		t.Error("pid 0 should have empty command line")
	}
	if CommandLine(99999999) != "" {
		t.Error("nonexistent pid should have empty command line")
	}
}

func TestMatches(t *testing.T) {
	skipIfNotLinux(t)
	if !Matches(os.Getpid(), "procscan") {
		t.Error("own pid should match the procscan marker")
	}
	if Matches(os.Getpid(), "definitely-not-a-marker-xyz") {
		t.Error("own pid should not match an arbitrary marker")
	}
	if Matches(os.Getpid(), "") {
		t.Error("empty marker should never match")
	}
	if Matches(99999999, "procscan") {
		t.Error("nonexistent pid should not match")
	}
}
