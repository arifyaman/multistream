package supervisor

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xlip/multistream/internal/mediamtx"
	"github.com/xlip/multistream/internal/state"
)

// moqMinVersion is the first mediamtx release with the MoQ subsystem (and
// with the "moq" config key). mediamtx rejects unknown keys, so generated
// configs must only mention moq when the binary understands it.
const moqMinVersion = "1.20.0"

// limiter bounds restarts of one supervised child: burst or more restarts
// within interval mark the child failed until a manual reset. The bounds are
// passed to over (rather than stored) so a supervisor's fields can be
// adjusted after construction, as tests do.
type limiter struct {
	mu sync.Mutex
	at []time.Time
}

func newLimiter() *limiter { return &limiter{} }

// over reports whether the limit is hit, pruning stale timestamps first.
func (l *limiter) over(interval time.Duration, burst int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-interval)
	kept := l.at[:0]
	for _, t := range l.at {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.at = kept
	return len(l.at) >= burst
}

func (l *limiter) record() {
	l.mu.Lock()
	l.at = append(l.at, time.Now())
	l.mu.Unlock()
}

func (l *limiter) reset() {
	l.mu.Lock()
	l.at = l.at[:0]
	l.mu.Unlock()
}

// relay is the supervisor's managed mediamtx relay.
type relay struct {
	mode   string // "spawned" (daemon owns the process) or "external"
	config string // generated mediamtx config path (spawned only)

	mu        sync.Mutex
	cmd       *exec.Cmd
	pid       int
	state     string
	restarts  int
	lastError string
	since     time.Time
	action    chan struct{}
	log       *ringLog
	lim       *limiter
}

func (r *relay) setCmd(c *exec.Cmd) {
	r.mu.Lock()
	r.cmd = c
	r.mu.Unlock()
}

func (r *relay) setPID(pid int) {
	r.mu.Lock()
	r.pid = pid
	if pid > 0 {
		r.since = time.Now()
	}
	r.mu.Unlock()
}

func (r *relay) setState(st, lastErr string) {
	r.mu.Lock()
	r.state = st
	if lastErr != "" {
		r.lastError = lastErr
	}
	r.mu.Unlock()
}

func (r *relay) recordRestart(lastErr string) {
	r.mu.Lock()
	r.restarts++
	if lastErr != "" {
		r.lastError = lastErr
	}
	r.mu.Unlock()
	r.lim.record()
}

func (r *relay) resetLimit() {
	r.lim.reset()
}

func (r *relay) kill() {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd != nil {
		killProcess(cmd)
	}
}

func (r *relay) waitForAction(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-r.action:
		return true
	}
}

func (r *relay) waitAfterFailure(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-r.action:
		r.resetLimit()
		return true
	case <-t.C:
		return true
	}
}

// startRelay probes the configured mediamtx API. If something is already
// serving it (an externally managed relay), the daemon tracks that relay but
// does not own it. Otherwise it generates a config and supervises a spawned
// one.
func (s *Supervisor) startRelay(ctx context.Context) {
	if s.apiReachable(ctx) {
		s.relay.mode = "external"
		s.relay.setState("running", "")
		s.relay.mu.Lock()
		s.relay.since = time.Now()
		s.relay.mu.Unlock()
		s.publishRelay()
		s.logf("relay: API %s already reachable; using the external relay (not spawned by this daemon)", s.cfg.MediaMTXAPI)
		return
	}
	version := ""
	if v, err := mediamtxVersion(s.relayBin); err == nil {
		version = v
	}
	if err := s.writeRelayConfig(version); err != nil {
		s.relay.setState("failed", err.Error())
		s.publishRelay()
		s.logf("relay: %v", err)
		return
	}
	s.logf("relay: spawning mediamtx (%s), config %s", versionOrUnknown(version), s.relay.config)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.superviseRelay(ctx)
	}()
}

// apiReachable reports whether a mediamtx API already answers at the
// configured address.
func (s *Supervisor) apiReachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client := mediamtx.NewClient(s.cfg.MediaMTXAPI)
	_, err := client.Version(ctx)
	return err == nil
}

// mediamtxVersion returns the version of a mediamtx binary
// (e.g. "1.20.1" from "v1.20.1").
var mediamtxVersionRe = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

func mediamtxVersion(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("probe mediamtx version: %w", err)
	}
	m := mediamtxVersionRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("unrecognized mediamtx version output %q", strings.TrimSpace(string(out)))
	}
	return string(m[1]), nil
}

func versionOrUnknown(v string) string {
	if v == "" {
		return "unknown version"
	}
	return "v" + v
}

// writeRelayConfig renders the mediamtx config for the managed relay into
// the state dir. Only the subsystems multistream needs are enabled: RTMP and
// the loopback API. The ingest path mirrors the multistream config; the away
// file, when configured, turns on mediamtx's always-available mode.
func (s *Supervisor) writeRelayConfig(version string) error {
	u, err := url.Parse(s.cfg.MediaMTXAPI)
	if err != nil || u.Host == "" {
		return fmt.Errorf("parse mediamtx_api %q: %v", s.cfg.MediaMTXAPI, err)
	}
	var b strings.Builder
	b.WriteString("# Generated by the multistream daemon - do not edit; changes are lost on (re)start.\n")
	fmt.Fprintf(&b, "api: yes\n")
	fmt.Fprintf(&b, "apiAddress: %s\n", u.Host)
	fmt.Fprintf(&b, "rtmp: yes\n")
	fmt.Fprintf(&b, "rtmpAddress: :%d\n", s.cfg.IngestPort)
	b.WriteString("hls: no\n")
	b.WriteString("rtsp: no\n")
	b.WriteString("srt: no\n")
	b.WriteString("webrtc: no\n")
	// Unknown version: assume the bundled (current) one, which knows moq.
	includeMoq := true
	if version != "" {
		if ok, err := mediamtx.VersionAtLeast(version, moqMinVersion); err == nil {
			includeMoq = ok
		}
	}
	if includeMoq {
		b.WriteString("moq: no\n")
	}
	b.WriteString("logLevel: info\n")
	b.WriteString("paths:\n")
	fmt.Fprintf(&b, "  %s:\n", s.cfg.IngestPath)
	b.WriteString("    source: publisher\n")
	if s.cfg.AwayFile != "" {
		b.WriteString("    alwaysAvailable: true\n")
		fmt.Fprintf(&b, "    alwaysAvailableFile: %s\n", s.cfg.AwayFile)
	}
	// Stock mediamtx allows publishing to any path via the "all" catch-all;
	// without it, newer mediamtx rejects publishes to unconfigured paths.
	b.WriteString("  all:\n")
	b.WriteString("    source: publisher\n")
	path := state.RelayConfigFile(s.dir)
	if err := state.WriteFile(path, []byte(b.String())); err != nil {
		return fmt.Errorf("write relay config: %w", err)
	}
	s.relay.config = path
	return nil
}

// superviseRelay is the run loop for the spawned relay, mirroring the
// per-platform loop: spawn, watch, restart with delay and rate limit.
func (s *Supervisor) superviseRelay(ctx context.Context) {
	r := s.relay
	for {
		if ctx.Err() != nil {
			return
		}
		if r.lim.over(s.limitInterval, s.limitBurst) {
			r.setState("failed", "restart limit reached; run 'multistream restart "+RelayName+"' to retry")
			s.publishRelay()
			if !r.waitForAction(ctx) {
				r.setState("stopped", "")
				s.publishRelay()
				return
			}
			r.resetLimit()
			continue
		}

		cmd := exec.Command(s.relayBin, r.config)
		cmd.Stdout = io.Discard
		cmd.Stderr = r.log
		configureChild(cmd)
		if err := cmd.Start(); err != nil {
			r.setState("failed", err.Error())
			s.publishRelay()
			if !r.waitAfterFailure(ctx, s.restartDelay) {
				r.setState("stopped", "")
				s.publishRelay()
				return
			}
			continue
		}

		r.setCmd(cmd)
		r.setPID(cmd.Process.Pid)
		_ = state.WriteFile(state.PidFile(s.dir, RelayName), []byte(strconv.Itoa(cmd.Process.Pid)))
		r.log.reset()
		r.setState("running", "")
		s.publishRelay()

		exitCh := make(chan error, 1)
		go func() { exitCh <- cmd.Wait() }()

		trigger := exit
		select {
		case <-ctx.Done():
			trigger = shutdown
			r.kill()
		case <-r.action:
			trigger = manual
			r.kill()
		case <-exitCh:
		}
		if trigger != exit {
			<-exitCh
		}

		r.setCmd(nil)
		r.setPID(0)
		os.Remove(state.PidFile(s.dir, RelayName))

		switch trigger {
		case shutdown:
			r.setState("stopped", "")
			s.publishRelay()
			return
		case manual:
			// User-requested restart: not a crash, so it does not count
			// against the limit; respawn immediately.
			r.resetLimit()
			r.setState("restarting", "")
			s.publishRelay()
			continue
		}

		r.recordRestart(r.log.lastLine())
		r.setState("restarting", "")
		s.publishRelay()
		if !r.waitAfterFailure(ctx, s.restartDelay) {
			r.setState("stopped", "")
			s.publishRelay()
			return
		}
	}
}

// publishRelay snapshots the relay into the published document.
func (s *Supervisor) publishRelay() {
	r := s.relay
	r.mu.Lock()
	rs := state.RelayState{
		Managed:   true,
		Mode:      r.mode,
		PID:       r.pid,
		Restarts:  r.restarts,
		State:     r.state,
		LastError: r.lastError,
		Since:     r.since,
	}
	r.mu.Unlock()
	s.mu.Lock()
	s.published.Updated = time.Now()
	s.published.Relay = &rs
	s.mu.Unlock()
	s.publish()
}

// logf writes a daemon-level message (not child output) to stderr.
func (s *Supervisor) logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "multistream daemon: "+format+"\n", args...)
}
