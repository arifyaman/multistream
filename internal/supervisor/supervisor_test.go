package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/procscan"
	"github.com/xlip/multistream/internal/state"
)

// --- pure helper tests ---------------------------------------------------

func TestExpandPushURLNoTemplate(t *testing.T) {
	got, secrets, err := expandPushURL("rtmp://h/app/key", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "rtmp://h/app/key" || len(secrets) != 0 {
		t.Errorf("got (%q, %v)", got, secrets)
	}
}

func TestExpandPushURLResolves(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "twitch.env")
	os.WriteFile(keyFile, []byte("TWITCH_KEY=live_secret_123\n"), 0o600)
	got, secrets, err := expandPushURL("rtmp://live.twitch.tv/app/${TWITCH_KEY}", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != "rtmp://live.twitch.tv/app/live_secret_123" {
		t.Errorf("got %q", got)
	}
	if len(secrets) != 1 || secrets[0] != "live_secret_123" {
		t.Errorf("secrets = %v", secrets)
	}
}

func TestExpandPushURLMissingKeyFile(t *testing.T) {
	if _, _, err := expandPushURL("rtmp://h/${KEY}", ""); err == nil {
		t.Error("want error when push_url has a template but no key file")
	}
}

func TestExpandPushURLMissingVariable(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "twitch.env")
	os.WriteFile(keyFile, []byte("OTHER=x\n"), 0o600)
	if _, _, err := expandPushURL("rtmp://h/${KEY}", keyFile); err == nil {
		t.Error("want error for a template with no matching key")
	}
}

func TestReadEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "k.env")
	os.WriteFile(p, []byte("# comment\nA=1\nB = \"quoted\" \nC='single'\n\nbadline\nD=\n"), 0o600)
	vars, err := readEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if vars["A"] != "1" || vars["B"] != "quoted" || vars["C"] != "single" || vars["D"] != "" {
		t.Errorf("vars = %v", vars)
	}
	if _, ok := vars["badline"]; ok {
		t.Error("line without = should be ignored")
	}
}

func TestRedact(t *testing.T) {
	got := redact("error at rtmp://h/app/live_secret_123", []string{"live_secret_123"})
	if strings.Contains(got, "live_secret_123") {
		t.Errorf("secret not redacted: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("want [redacted] marker: %q", got)
	}
}

// --- supervision loop tests ----------------------------------------------

// skipIfNotLinux skips the supervision loop tests elsewhere: they stand in
// for ffmpeg with /bin/sh scripts and assert on the /proc-based liveness
// checks the daemon performs on Linux.
func skipIfNotLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("supervision loop tests need /bin/sh and /proc; Linux only")
	}
}

// writeFakeFFmpeg writes a shell script that ignores its arguments and runs
// body, standing in for the ffmpeg binary.
func writeFakeFFmpeg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakeffmpeg.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestSupervisor(t *testing.T, ffmpeg string, restartDelay, limitInterval time.Duration, burst int) *Supervisor {
	t.Helper()
	cfg := &config.Config{
		MediaMTXAPI: "http://127.0.0.1:9997",
		IngestPath:  "live/test",
		IngestPort:  1935,
		RefreshSec:  2,
		FFmpegPath:  ffmpeg,
		Platforms:   []config.Platform{{Name: "twitch", PushURL: "rtmp://example.com/live/k"}},
	}
	sup, err := New(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sup.restartDelay = restartDelay
	sup.limitInterval = limitInterval
	sup.limitBurst = burst
	return sup
}

// tryPublishedState returns the platform state the supervisor last published
// and whether it is available yet.
func tryPublishedState(t *testing.T, sup *Supervisor, name string) (state.PlatformState, bool) {
	t.Helper()
	st, err := state.LoadSupervisorState(sup.dir)
	if err != nil {
		t.Fatalf("load supervisor state: %v", err)
	}
	if st == nil {
		return state.PlatformState{}, false
	}
	ps, ok := st.Platforms[name]
	return ps, ok
}

func waitForPlatform(t *testing.T, sup *Supervisor, name, wantState string, minRestarts int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ps, ok := tryPublishedState(t, sup, name); ok {
			if (wantState == "" || ps.State == wantState) && ps.Restarts >= minRestarts {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	ps, _ := tryPublishedState(t, sup, name)
	t.Fatalf("timed out: platform %s state=%q restarts=%d, want state=%q restarts>=%d",
		name, ps.State, ps.Restarts, wantState, minRestarts)
}

// runSupervisor starts the supervisor in a goroutine and returns a cancel
// func plus a wait func that blocks until Start has fully returned (i.e. the
// supervisor has cleaned up).
func runSupervisor(t *testing.T, sup *Supervisor) (cancel func(), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Start(ctx) }()
	wait = func() {
		err := <-done
		if err != nil {
			t.Errorf("supervisor Start returned %v", err)
		}
	}
	return cancel, wait
}

func TestSupervisorRunsAndStops(t *testing.T) {
	skipIfNotLinux(t)
	ffmpeg := writeFakeFFmpeg(t, "sleep 60")
	sup := newTestSupervisor(t, ffmpeg, 30*time.Millisecond, time.Second, 5)
	cancel, wait := runSupervisor(t, sup)
	defer cancel()

	waitForPlatform(t, sup, "twitch", "running", 0, 3*time.Second)
	ps, ok := tryPublishedState(t, sup, "twitch")
	if !ok {
		t.Fatal("twitch state not published")
	}
	if ps.PID <= 0 || !procscan.Alive(ps.PID) {
		t.Errorf("expected a live pid, got %d", ps.PID)
	}
	if _, err := os.Stat(state.PidFile(sup.dir, "twitch")); err != nil {
		t.Errorf("pid file missing: %v", err)
	}

	cancel()
	wait()
	if ps, _ := tryPublishedState(t, sup, "twitch"); ps.State != "stopped" {
		t.Errorf("state after stop = %q, want stopped", ps.State)
	}
	if _, err := os.Stat(state.PidFile(sup.dir, "twitch")); !os.IsNotExist(err) {
		t.Errorf("pid file should be removed after stop, stat err=%v", err)
	}
}

func TestSupervisorRestartsOnExit(t *testing.T) {
	skipIfNotLinux(t)
	ffmpeg := writeFakeFFmpeg(t, "exit 0")
	sup := newTestSupervisor(t, ffmpeg, 30*time.Millisecond, time.Minute, 100)
	cancel, wait := runSupervisor(t, sup)
	defer cancel()

	// The fake exits immediately, so the supervisor must restart it several
	// times on its own.
	waitForPlatform(t, sup, "twitch", "", 3, 5*time.Second)

	cancel()
	wait()
}

func TestSupervisorStartLimitAndManualRestart(t *testing.T) {
	skipIfNotLinux(t)
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	// Fails its first four invocations, then stays up (stands in for a
	// platform whose outage clears after a manual nudge).
	body := fmt.Sprintf(`n=$(cat "%s" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "%s"
if [ "$n" -lt 5 ]; then exit 1; fi
sleep 60
`, counter, counter)
	script := filepath.Join(dir, "fakeffmpeg.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := newTestSupervisor(t, script, 20*time.Millisecond, time.Second, 3)
	cancel, wait := runSupervisor(t, sup)
	defer cancel()

	// Three quick failures within the window must trip the limit.
	waitForPlatform(t, sup, "twitch", "failed", 3, 5*time.Second)

	// A manual restart resets the limit; the fake succeeds on its 5th
	// attempt, so the platform reaches and holds "running".
	if err := sup.Restart("twitch"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitForPlatform(t, sup, "twitch", "running", 0, 5*time.Second)

	cancel()
	wait()
}

func TestSupervisorRestartUnknownPlatform(t *testing.T) {
	sup := newTestSupervisor(t, writeFakeFFmpeg(t, "sleep 60"), 30*time.Millisecond, time.Second, 5)
	if err := sup.Restart("nope"); err == nil {
		t.Error("want error for unknown platform")
	}
}
