package daemonipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// pipeSeq gives each Windows test a unique pipe name: named pipes live in a
// machine-wide namespace shared by every process.
var pipeSeq int32

// testEndpoint returns a network/address pair for a test server: a per-test
// socket file on Unix, a uniquely named pipe on Windows.
func testEndpoint(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return "tcp", fmt.Sprintf(`\\.\pipe\multistream-test-%d-%d`, os.Getpid(), atomic.AddInt32(&pipeSeq, 1))
	}
	return "unix", filepath.Join(t.TempDir(), "test.sock")
}

func startServer(t *testing.T, restart RestartFunc) (string, *Server) {
	t.Helper()
	network, addr := testEndpoint(t)
	s := NewServer(network, addr, restart)
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return network, s
}

func TestPing(t *testing.T) {
	network, s := startServer(t, nil)
	if err := Ping(network, s.Addr()); err != nil {
		t.Errorf("Ping to live server: %v", err)
	}
}

func TestPingNoServer(t *testing.T) {
	network, addr := testEndpoint(t)
	if err := Ping(network, addr); err == nil {
		t.Error("Ping to absent endpoint should fail")
	}
}

func TestRestartCallsHandler(t *testing.T) {
	var calls atomic.Int32
	var last string
	network, s := startServer(t, func(platform string) error {
		calls.Add(1)
		last = platform
		return nil
	})
	if err := Restart(network, s.Addr(), "twitch"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if calls.Load() != 1 || last != "twitch" {
		t.Errorf("handler calls=%d last=%q, want 1/twitch", calls.Load(), last)
	}
}

func TestRestartHandlerError(t *testing.T) {
	network, s := startServer(t, func(platform string) error {
		return errors.New("kaboom")
	})
	if err := Restart(network, s.Addr(), "twitch"); err == nil {
		t.Error("Restart should surface handler error")
	}
}

func TestRestartNoHandler(t *testing.T) {
	network, s := startServer(t, nil)
	if err := Restart(network, s.Addr(), "twitch"); err == nil {
		t.Error("Restart with no handler should fail")
	}
}

func TestUnknownOp(t *testing.T) {
	network, s := startServer(t, nil)
	resp, err := Do(network, s.Addr(), Request{Op: "nonsense"})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if resp.OK {
		t.Error("unknown op should be rejected (OK=false)")
	}
}

func TestSingleInstanceGuard(t *testing.T) {
	network, addr := testEndpoint(t)
	first := NewServer(network, addr, nil)
	if err := first.Listen(); err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer first.Close()

	second := NewServer(network, addr, nil)
	if err := second.Listen(); err == nil {
		second.Close()
		t.Error("second Listen on the same endpoint should fail (single-instance guard)")
	}
}
