// Package supervisor runs and watches the per-platform ffmpeg
// re-broadcasters inside the multistream daemon, replacing the old one
// systemd unit per platform. For each platform it owns the ffmpeg process:
// it spawns it, restarts it on exit (with a delay and a start-rate limit
// mirroring systemd's Restart=/StartLimit=), remembers the last error, and
// publishes its state so the read-only commands can report on it. It also
// clears orphaned ffmpeg left behind by a previously killed daemon so a
// platform is never pushed twice.
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/daemonipc"
	"github.com/xlip/multistream/internal/procscan"
	"github.com/xlip/multistream/internal/state"
)

// varRe matches ${NAME} templates in a push URL.
var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// RelayName is the user-facing name of the managed mediamtx relay: it is the
// argument to `multistream restart` and the key in the state document.
const RelayName = "relay"

// Supervisor owns the supervised ffmpeg processes for a set of platforms,
// and - when manage_mediamtx is set - the mediamtx relay itself.
type Supervisor struct {
	cfg    *config.Config
	dir    string
	ffmpeg string
	input  string

	restartDelay  time.Duration
	limitInterval time.Duration
	limitBurst    int

	relayBin string
	relay    *relay

	platforms map[string]*platform
	order     []string

	ipc *daemonipc.Server
	wg  sync.WaitGroup

	mu        sync.Mutex
	published *state.SupervisorState
}

// New builds a Supervisor from cfg, resolving each platform's push URL from
// its key file. It fails fast if a push URL needs a key that is not
// available (the daemon cannot start that platform without it) or if a
// required runtime binary (ffmpeg, mediamtx when managed) cannot be found.
func New(cfg *config.Config, dir string) (*Supervisor, error) {
	ffmpeg, ok := config.ResolveBinary(cfg.FFmpegPath, "ffmpeg")
	if !ok {
		return nil, fmt.Errorf("no ffmpeg binary found (set ffmpeg_path, put ffmpeg on PATH, or install via the npm package)")
	}
	s := &Supervisor{
		cfg:           cfg,
		dir:           dir,
		ffmpeg:        ffmpeg,
		input:         cfg.InputURL(),
		restartDelay:  time.Duration(cfg.RestartSec) * time.Second,
		limitInterval: time.Duration(cfg.StartLimitIntervalSec) * time.Second,
		limitBurst:    cfg.StartLimitBurst,
		platforms:     map[string]*platform{},
	}
	s.published = &state.SupervisorState{Platforms: map[string]state.PlatformState{}}
	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		plat := &platform{
			name:   p.Name,
			state:  "stopped",
			log:    newRingLog(64 << 10),
			action: make(chan struct{}, 1),
			lim:    newLimiter(),
		}
		expanded, secrets, err := expandPushURL(p.PushURL, cfg.KeyFile(p))
		if err != nil {
			return nil, fmt.Errorf("platform %q: %w", p.Name, err)
		}
		plat.pushURL = expanded
		plat.secrets = secrets
		plat.args = []string{
			"-hide_banner", "-loglevel", "warning",
			"-i", s.input,
			"-c", "copy", "-f", "flv",
			expanded,
		}
		s.platforms[p.Name] = plat
		s.order = append(s.order, p.Name)
		s.published.Platforms[p.Name] = state.PlatformState{State: "stopped"}
	}
	if cfg.ManageMediaMTX {
		bin, ok := config.ResolveBinary(cfg.MediaMTXPath, "mediamtx")
		if !ok {
			return nil, fmt.Errorf("manage_mediamtx is set but no mediamtx binary was found (set mediamtx_path, put mediamtx on PATH, or install via the npm package)")
		}
		s.relayBin = bin
		s.relay = &relay{
			mode:   "spawned",
			state:  "stopped",
			log:    newRingLog(64 << 10),
			action: make(chan struct{}, 1),
			lim:    newLimiter(),
		}
	}
	return s, nil
}

// Start runs the supervisor until ctx is done, then stops it gracefully.
// It binds the IPC endpoint first, which doubles as the single-instance
// guard: a second daemon fails to start while one is running.
func (s *Supervisor) Start(ctx context.Context) error {
	network, addr := state.IPCNetworkAddr(s.dir)
	s.ipc = daemonipc.NewServer(network, addr, s.Restart)
	if err := s.ipc.Listen(); err != nil {
		return fmt.Errorf("start IPC endpoint (is another multistream daemon running?): %w", err)
	}

	pid := os.Getpid()
	s.mu.Lock()
	s.published.DaemonPID = pid
	s.mu.Unlock()
	if err := state.WriteFile(state.DaemonPidFile(s.dir), []byte(strconv.Itoa(pid))); err != nil {
		s.ipc.Close()
		return err
	}

	// Clear orphans from a previously killed daemon before spawning, so we
	// never end up with two ffmpeg for one platform.
	for _, name := range s.order {
		s.cleanupOrphan(s.platforms[name])
	}
	s.publish()

	// Start the relay before the re-broadcasters so their first pull has
	// something to connect to (an ffmpeg that arrives early simply retries).
	if s.relay != nil {
		s.startRelay(ctx)
	}

	for _, name := range s.order {
		s.wg.Add(1)
		go func(n string) {
			defer s.wg.Done()
			s.supervise(n, ctx)
		}(name)
	}

	<-ctx.Done()
	s.wg.Wait() // let each goroutine reap its own child
	if s.ipc != nil {
		s.ipc.Close()
	}
	os.Remove(state.DaemonPidFile(s.dir))
	return nil
}

// Restart asks one platform (or the managed relay, by its RelayName) to
// (re)start immediately. It resets the target's start-limit counter and is
// safe to call from any state.
func (s *Supervisor) Restart(name string) error {
	if name == RelayName {
		if s.relay == nil {
			return fmt.Errorf("the relay is not managed by this daemon (manage_mediamtx is off)")
		}
		if s.relay.mode != "spawned" {
			return fmt.Errorf("the relay is external (a mediamtx API was already reachable at daemon start); restart it where it runs")
		}
		select {
		case s.relay.action <- struct{}{}:
		default: // a restart is already pending
		}
		return nil
	}
	p, ok := s.platforms[name]
	if !ok {
		return fmt.Errorf("unknown platform %q", name)
	}
	select {
	case p.action <- struct{}{}:
	default: // a restart is already pending
	}
	return nil
}

// supervise is the per-platform run loop, executed in its own goroutine.
func (s *Supervisor) supervise(name string, ctx context.Context) {
	p := s.platforms[name]
	for {
		if ctx.Err() != nil {
			return
		}
		if s.overLimit(p) {
			p.setState("failed", "restart limit reached; run 'multistream restart "+p.name+"' to retry")
			s.publishPlatform(p)
			if !p.waitForAction(ctx) {
				p.setState("stopped", "")
				s.publishPlatform(p)
				return
			}
			p.resetLimit()
			continue
		}

		cmd, err := s.spawn(p)
		if err != nil {
			p.setState("failed", redact(err.Error(), p.secrets))
			s.publishPlatform(p)
			if !p.waitAfterFailure(ctx, s.restartDelay) {
				p.setState("stopped", "")
				s.publishPlatform(p)
				return
			}
			continue
		}

		p.setCmd(cmd)
		p.setPID(cmd.Process.Pid)
		s.writePid(p)
		p.log.reset()
		p.setState("running", "")
		s.publishPlatform(p)

		exitCh := make(chan error, 1)
		go func() { exitCh <- cmd.Wait() }()

		trigger := exit
		select {
		case <-ctx.Done():
			trigger = shutdown
			p.kill()
		case <-p.action:
			trigger = manual
			p.kill()
		case <-exitCh:
		}
		if trigger != exit {
			<-exitCh
		}

		p.setCmd(nil)
		p.setPID(0)
		s.removePid(p)

		switch trigger {
		case shutdown:
			p.setState("stopped", "")
			s.publishPlatform(p)
			return
		case manual:
			// User-requested restart: not a crash, so it does not count
			// against the limit; respawn immediately.
			p.resetLimit()
			p.setState("restarting", "")
			s.publishPlatform(p)
			continue
		}

		p.recordRestart(redact(p.log.lastLine(), p.secrets))
		p.setState("restarting", "")
		s.publishPlatform(p)
		if !p.waitAfterFailure(ctx, s.restartDelay) {
			p.setState("stopped", "")
			s.publishPlatform(p)
			return
		}
	}
}

// spawn starts one ffmpeg for platform p.
func (s *Supervisor) spawn(p *platform) (*exec.Cmd, error) {
	cmd := exec.Command(s.ffmpeg, p.args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = p.log
	configureChild(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// cleanupOrphan kills a stale ffmpeg for p left behind by a previously
// killed daemon (or any ffmpeg whose pid file points at one of ours), so
// the daemon never ends up with two re-broadcasters for a platform.
func (s *Supervisor) cleanupOrphan(p *platform) {
	path := state.PidFile(s.dir, p.name)
	pid, ok := state.ReadPid(path)
	if !ok {
		return
	}
	if !procscan.Matches(pid, s.input) {
		os.Remove(path) // dead or recycled pid; just clear it
		return
	}
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && procscan.Alive(pid) {
		time.Sleep(50 * time.Millisecond)
	}
	os.Remove(path)
}

func (s *Supervisor) writePid(p *platform) {
	pid := p.pID()
	if pid <= 0 {
		return
	}
	// Best effort: the pid file only serves the stateless status fallback.
	_ = state.WriteFile(state.PidFile(s.dir, p.name), []byte(strconv.Itoa(pid)))
}

func (s *Supervisor) removePid(p *platform) {
	os.Remove(state.PidFile(s.dir, p.name))
}

// overLimit reports whether p has restarted limitBurst or more times within
// the limit interval, pruning stale restart timestamps first.
func (s *Supervisor) overLimit(p *platform) bool {
	return p.lim.over(s.limitInterval, s.limitBurst)
}

// publishPlatform snapshots p into the published document and writes it.
func (s *Supervisor) publishPlatform(p *platform) {
	p.mu.Lock()
	ps := state.PlatformState{
		PID:       p.pid,
		Restarts:  p.restarts,
		State:     p.state,
		LastError: p.lastError,
		Since:     p.since,
	}
	p.mu.Unlock()
	s.mu.Lock()
	s.published.Updated = time.Now()
	s.published.Platforms[p.name] = ps
	s.mu.Unlock()
	s.publish()
}

func (s *Supervisor) publish() {
	s.mu.Lock()
	data, err := json.Marshal(s.published)
	s.mu.Unlock()
	if err != nil {
		return
	}
	data = append(data, '\n')
	// Best effort: readers tolerate a missing or stale state document.
	_ = state.WriteFile(state.SupervisorFile(s.dir), data)
}

// platform is the supervisor's per-platform runtime state.
type platform struct {
	name    string
	pushURL string
	secrets []string
	args    []string

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

func (p *platform) setCmd(c *exec.Cmd) {
	p.mu.Lock()
	p.cmd = c
	p.mu.Unlock()
}

func (p *platform) setPID(pid int) {
	p.mu.Lock()
	p.pid = pid
	if pid > 0 {
		p.since = time.Now()
	}
	p.mu.Unlock()
}

func (p *platform) pID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *platform) setState(st, lastErr string) {
	p.mu.Lock()
	p.state = st
	if lastErr != "" {
		p.lastError = lastErr
	}
	p.mu.Unlock()
}

func (p *platform) recordRestart(lastErr string) {
	p.mu.Lock()
	p.restarts++
	if lastErr != "" {
		p.lastError = lastErr
	}
	p.mu.Unlock()
	p.lim.record()
}

func (p *platform) resetLimit() {
	p.lim.reset()
}

func (p *platform) kill() {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd != nil {
		killProcess(cmd)
	}
}

// waitForAction blocks until a manual restart is requested (true) or the
// context is done (false).
func (p *platform) waitForAction(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-p.action:
		return true
	}
}

// waitAfterFailure blocks for d before the next respawn attempt, returning
// false when the context is done and true otherwise (a manual restart
// during the wait also resets the limit and returns true early).
func (p *platform) waitAfterFailure(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-p.action:
		p.resetLimit()
		return true
	case <-t.C:
		return true
	}
}

// expandPushURL resolves ${NAME} templates in pushURL from the platform's
// key file. It returns the expanded URL and the key values (used to redact
// logs). A push URL without templates needs no key file.
func expandPushURL(pushURL, keyFile string) (string, []string, error) {
	if !varRe.MatchString(pushURL) {
		return pushURL, nil, nil
	}
	if keyFile == "" {
		return "", nil, fmt.Errorf("push_url uses ${...} but no key file is configured (set keys_dir)")
	}
	vars, err := readEnvFile(keyFile)
	if err != nil {
		return "", nil, err
	}
	var missing []string
	expanded := varRe.ReplaceAllStringFunc(pushURL, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return "", nil, fmt.Errorf("key file %s missing variable(s): %s", keyFile, strings.Join(missing, ", "))
	}
	secrets := make([]string, 0, len(vars))
	for _, v := range vars {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	return expanded, secrets, nil
}

// readEnvFile parses a KEY=VALUE env file (the 0600 per-platform key file).
func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", path, err)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		vars[k] = strings.Trim(v, `"'`)
	}
	return vars, nil
}

// redact replaces every secret in s with [redacted].
func redact(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}
