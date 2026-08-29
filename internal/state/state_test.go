package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDirPathEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envState, dir)
	got, err := DirPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("DirPath = %q, want %q", got, dir)
	}
	// StateDir must return the same path and create it.
	got2, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got2 != dir {
		t.Errorf("StateDir = %q, want %q", got2, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("StateDir did not create dir: %v", err)
	}
}

func TestFilePaths(t *testing.T) {
	dir := "/some/dir"
	if got := PidFile(dir, "twitch"); got != filepath.Join(dir, "twitch.pid") {
		t.Errorf("PidFile = %q", got)
	}
	if got := DaemonPidFile(dir); got != filepath.Join(dir, "daemon.pid") {
		t.Errorf("DaemonPidFile = %q", got)
	}
	if got := SupervisorFile(dir); got != filepath.Join(dir, "supervisor.json") {
		t.Errorf("SupervisorFile = %q", got)
	}
}

func TestIPCNetworkAddr(t *testing.T) {
	network, addr := IPCNetworkAddr("/some/dir")
	if runtime.GOOS == "windows" {
		if network != "tcp" || addr != `\\.\pipe\multistream` {
			t.Errorf("got (%q, %q), want the named pipe endpoint", network, addr)
		}
		return
	}
	if network != "unix" {
		t.Errorf("network = %q, want unix", network)
	}
	if !filepath.IsAbs(addr) || filepath.Ext(addr) != ".sock" {
		t.Errorf("addr = %q, want an absolute .sock path", addr)
	}
}

func TestWriteFileAndLoadSupervisorState(t *testing.T) {
	dir := t.TempDir()
	st := &SupervisorState{
		Updated:   time.Now(),
		DaemonPID: 1234,
		Platforms: map[string]PlatformState{
			"twitch": {PID: 2000, Restarts: 2, State: "running", LastError: "boom"},
		},
	}
	data := mustMarshal(t, st)
	if err := WriteFile(SupervisorFile(dir), data); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSupervisorState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DaemonPID != 1234 {
		t.Errorf("DaemonPID = %d", got.DaemonPID)
	}
	p, ok := got.Platforms["twitch"]
	if !ok || p.PID != 2000 || p.Restarts != 2 || p.State != "running" || p.LastError != "boom" {
		t.Errorf("platform state = %+v", p)
	}
	if !got.IsFresh(time.Minute) {
		t.Error("fresh state should be IsFresh")
	}
}

func TestLoadSupervisorStateMissing(t *testing.T) {
	got, err := LoadSupervisorState(t.TempDir())
	if err != nil {
		t.Fatalf("err = %v, want nil for missing file", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestIsFreshStale(t *testing.T) {
	st := &SupervisorState{Updated: time.Now().Add(-time.Hour)}
	if st.IsFresh(time.Minute) {
		t.Error("hour-old state should not be fresh within a minute")
	}
	var nilSt *SupervisorState
	if nilSt.IsFresh(time.Minute) {
		t.Error("nil state should not be fresh")
	}
}

func TestReadPid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.pid")
	if _, ok := ReadPid(p); ok {
		t.Error("missing pid file should not parse")
	}
	os.WriteFile(p, []byte("4321"), 0o600)
	n, ok := ReadPid(p)
	if !ok || n != 4321 {
		t.Errorf("ReadPid = (%d, %v), want (4321, true)", n, ok)
	}
	os.WriteFile(p, []byte("not-a-number"), 0o600)
	if _, ok := ReadPid(p); ok {
		t.Error("non-numeric pid file should not parse")
	}
	os.WriteFile(p, []byte("0"), 0o600)
	if _, ok := ReadPid(p); ok {
		t.Error("zero pid should not parse")
	}
}
