// Package report assembles the full status picture (ingest + platforms)
// and renders it as a terminal table or JSON. It is daemon-optional: the
// ingest line always comes from the mediamtx API, and each platform line is
// built from live process inspection plus, when the daemon is running, the
// supervisor's richer state (restart count, last error, failed state).
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/daemonipc"
	"github.com/xlip/multistream/internal/mediamtx"
	"github.com/xlip/multistream/internal/netmon"
	"github.com/xlip/multistream/internal/procscan"
	"github.com/xlip/multistream/internal/state"
)

// sampleInterval is the double-sample window used to measure ingest
// bitrate on the first (one-shot) collect.
const sampleInterval = time.Second

// IngestStatus is the normalized state of the OBS -> mediamtx link.
type IngestStatus struct {
	APIError       string     `json:"api_error,omitempty"`
	Online         bool       `json:"online"`
	Available      bool       `json:"available"`
	Kbps           int        `json:"kbps,omitempty"`
	Bytes          uint64     `json:"bytes_received,omitempty"`
	Readers        int        `json:"readers"`
	Resolution     string     `json:"resolution,omitempty"`
	VideoCodec     string     `json:"video_codec,omitempty"`
	AudioCodec     string     `json:"audio_codec,omitempty"`
	OnlineSince    *time.Time `json:"online_since,omitempty"`
	AvailableSince *time.Time `json:"available_since,omitempty"`
}

// State returns the ingest line state: "live" (a real publisher is
// connected), "away" (readable, but only the offline away segment is
// playing) or "down" (API error, or nothing to read).
func (s IngestStatus) State() string {
	switch {
	case s.APIError != "":
		return "down"
	case s.Online:
		return "live"
	case s.Available:
		return "away"
	default:
		return "down"
	}
}

// PlatformStatus is the normalized state of one re-broadcaster.
type PlatformStatus struct {
	Name      string     `json:"name"`
	Managed   bool       `json:"managed"` // the daemon is running and manages it
	Running   bool       `json:"running"` // its ffmpeg process is alive
	PID       int        `json:"pid,omitempty"`
	Connected bool       `json:"connected"` // actually pulling from the relay
	ConnErr   string     `json:"connected_error,omitempty"`
	State     string     `json:"supervisor_state,omitempty"` // running/restarting/failed/stopped
	Restarts  int        `json:"restarts,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// Good reports whether the platform is fully healthy: its ffmpeg process is
// alive and actually pulling from the relay.
func (p PlatformStatus) Good() bool {
	return p.Running && p.Connected && p.ConnErr == ""
}

// StatusReport is the full picture the CLI prints.
type StatusReport struct {
	Time            time.Time        `json:"time"`
	DaemonUp        bool             `json:"daemon_up"`
	Ingest          IngestStatus     `json:"ingest"`
	Platforms       []PlatformStatus `json:"platforms"`
	ExpectedReaders int              `json:"expected_readers"`
	OK              bool             `json:"ok"`
}

// Collector keeps sampling state between Collect calls (bitrate deltas) and
// knows where the daemon's state lives.
type Collector struct {
	cfg       *config.Config
	client    *mediamtx.Client
	stateDir  string
	lastBytes uint64
	lastTime  time.Time
}

// NewCollector builds a Collector for the given config. stateDir is where
// the daemon writes its pid files and state document ("" disables the
// daemon-aware lookups).
func NewCollector(cfg *config.Config, stateDir string) *Collector {
	return &Collector{cfg: cfg, client: mediamtx.NewClient(cfg.MediaMTXAPI), stateDir: stateDir}
}

// daemonUp reports whether a daemon is currently serving IPC.
func (c *Collector) daemonUp() bool {
	if c.stateDir == "" {
		return false
	}
	network, addr := state.IPCNetworkAddr(c.stateDir)
	return daemonipc.Ping(network, addr) == nil
}

// Collect takes one snapshot of the whole chain.
func (c *Collector) Collect(ctx context.Context) (*StatusReport, error) {
	up := c.daemonUp()
	rep := &StatusReport{Time: time.Now(), DaemonUp: up, ExpectedReaders: len(c.cfg.Platforms)}
	c.collectIngest(ctx, rep)
	for _, p := range c.cfg.Platforms {
		rep.Platforms = append(rep.Platforms, c.collectPlatform(p, up))
	}
	// Healthy means the ingest path can be read - live, or the away
	// segment when off-air - and every platform is pulling.
	rep.OK = rep.Ingest.APIError == "" && rep.Ingest.Available &&
		rep.Ingest.Readers >= rep.ExpectedReaders
	for _, ps := range rep.Platforms {
		if !ps.Good() {
			rep.OK = false
		}
	}
	return rep, nil
}

func (c *Collector) collectIngest(ctx context.Context, rep *StatusReport) {
	path, err := c.client.GetPath(ctx, c.cfg.IngestPath)
	if err != nil {
		rep.Ingest.APIError = err.Error()
		return
	}
	info := path.Info()
	rep.Ingest.Online = info.Online
	rep.Ingest.Available = info.Available
	rep.Ingest.Readers = info.Readers
	rep.Ingest.Resolution = info.Resolution
	rep.Ingest.VideoCodec = info.VideoCodec
	rep.Ingest.AudioCodec = info.AudioCodec
	rep.Ingest.Bytes = info.InboundBytes
	if !info.OnlineSince.IsZero() {
		ts := info.OnlineSince
		rep.Ingest.OnlineSince = &ts
	}
	if !info.AvailableSince.IsZero() {
		ts := info.AvailableSince
		rep.Ingest.AvailableSince = &ts
	}

	// Bitrate = delta of inbound bytes. First call samples twice so a
	// one-shot `status` still reports a number; later calls (watch mode)
	// reuse the previous sample.
	now := time.Now()
	if c.lastTime.IsZero() {
		time.Sleep(sampleInterval)
		if path2, err2 := c.client.GetPath(ctx, c.cfg.IngestPath); err2 == nil {
			info2 := path2.Info()
			rep.Ingest.Bytes = info2.InboundBytes
			if d := int64(info2.InboundBytes - info.InboundBytes); d > 0 {
				rep.Ingest.Kbps = int(float64(d) * 8 / 1000 / sampleInterval.Seconds())
			}
			now = time.Now()
		}
	} else if !now.After(c.lastTime) {
		now = time.Now()
	} else if d := int64(info.InboundBytes - c.lastBytes); d > 0 {
		rep.Ingest.Kbps = int(float64(d) * 8 / 1000 / now.Sub(c.lastTime).Seconds())
	}
	c.lastBytes = rep.Ingest.Bytes
	c.lastTime = now
}

func (c *Collector) collectPlatform(p config.Platform, daemonUp bool) PlatformStatus {
	ps := PlatformStatus{Name: p.Name, Managed: daemonUp}
	linux := runtime.GOOS == "linux"

	if daemonUp {
		// The daemon is the source of truth: it is the parent of each ffmpeg
		// and knows when a child dies, so its "running" state is reliable on
		// every OS.
		if sup := c.supervisor(); sup != nil {
			if s, ok := sup.Platforms[p.Name]; ok {
				ps.State = s.State
				ps.Restarts = s.Restarts
				ps.LastError = s.LastError
				if !s.Since.IsZero() {
					ps.Since = &s.Since
				}
				if s.State == "running" && s.PID > 0 {
					ps.Running = true
					ps.PID = s.PID
					if linux {
						// Precise per-PID check: is it actually pulling?
						if ok, err := netmon.PIDConnectedTo(s.PID, "127.0.0.1", c.cfg.IngestPort); err != nil {
							ps.ConnErr = err.Error()
						} else {
							ps.Connected = ok
						}
					} else {
						// The per-PID connection check is /proc-based (Linux).
						// Elsewhere we trust the daemon: an ffmpeg whose input
						// is the relay would crash and be restarted if it were
						// not connected, so "running" implies it is pulling.
						ps.Connected = true
					}
				}
			}
		}
	} else if linux && c.stateDir != "" {
		// No daemon, on Linux: fall back to the pid file (an orphaned ffmpeg,
		// if any). The /proc-based lookups do not work on other OSes, so
		// there is no stateless platform view without the daemon there.
		if id, ok := state.ReadPid(state.PidFile(c.stateDir, p.Name)); ok {
			if procscan.Alive(id) && procscan.Matches(id, c.cfg.InputURL()) {
				ps.Running = true
				ps.PID = id
				if ok, err := netmon.PIDConnectedTo(id, "127.0.0.1", c.cfg.IngestPort); err != nil {
					ps.ConnErr = err.Error()
				} else {
					ps.Connected = ok
				}
			}
		}
	}
	return ps
}

// supervisor loads the daemon's published state document, or nil when there
// is none.
func (c *Collector) supervisor() *state.SupervisorState {
	if c.stateDir == "" {
		return nil
	}
	st, err := state.LoadSupervisorState(c.stateDir)
	if err != nil {
		return nil
	}
	return st
}

// Events returns human-readable transition lines between prev and the
// current report (for watch mode).
func (r *StatusReport) Events(prev *StatusReport) []string {
	if prev == nil {
		return nil
	}
	var out []string
	now := time.Now().Format("15:04:05")
	if st := r.Ingest.State(); prev.Ingest.State() != st {
		switch st {
		case "live":
			out = append(out, fmt.Sprintf("%s ingest PUBLISHING", now))
		case "away":
			out = append(out, fmt.Sprintf("%s ingest AWAY (offline segment, waiting for publisher)", now))
		default:
			out = append(out, fmt.Sprintf("%s ingest DROPPED", now))
		}
	}
	oldByName := map[string]PlatformStatus{}
	for _, p := range prev.Platforms {
		oldByName[p.Name] = p
	}
	for _, p := range r.Platforms {
		old, had := oldByName[p.Name]
		if !had || old.Good() != p.Good() {
			if p.Good() {
				out = append(out, fmt.Sprintf("%s %s UP", now, p.Name))
			} else {
				out = append(out, fmt.Sprintf("%s %s DOWN (%s)", now, p.Name, downReason(p)))
			}
		}
	}
	return out
}

func downReason(p PlatformStatus) string {
	switch {
	case p.Running && p.ConnErr != "":
		return "connection check failed"
	case p.Running && !p.Connected:
		return "not connected to relay"
	case p.State != "":
		if p.LastError != "" {
			return p.State + ", " + p.LastError
		}
		return p.State
	case !p.Managed:
		return "daemon not running"
	default:
		return "not running"
	}
}

// Render renders the report as a plain text table.
func (r *StatusReport) Render(color bool) string {
	var b strings.Builder
	width := 8
	for _, p := range r.Platforms {
		if len(p.Name) > width {
			width = len(p.Name)
		}
	}
	state := func(s string) string {
		if !color {
			return s
		}
		switch s {
		case "UP":
			return cGreen + s + cReset
		case "AWAY":
			return cYellow + s + cReset
		case "DOWN":
			return cRed + s + cReset
		}
		return s
	}
	row := func(name, st, detail string) {
		fmt.Fprintf(&b, "%-*s  %s  %s\n", width, name, state(st), detail)
	}

	i := r.Ingest
	ingSt, ingDetail := "DOWN", "no publisher"
	switch i.State() {
	case "live":
		ingSt = "UP"
		ingDetail = ingestDetail(i, r.ExpectedReaders, "up", i.OnlineSince)
	case "away":
		ingSt = "AWAY"
		ingDetail = ingestDetail(i, r.ExpectedReaders, "away", i.AvailableSince)
	case "down":
		if i.APIError != "" {
			ingDetail = "api error: " + i.APIError
		}
	}
	row("ingest", ingSt, ingDetail)

	for _, p := range r.Platforms {
		st := "DOWN"
		if p.Good() {
			st = "UP"
		}
		row(p.Name, st, platformDetail(p))
	}
	if !r.DaemonUp {
		b.WriteString("daemon    not running (run 'multistream daemon' to supervise the re-broadcasters)\n")
	}
	return b.String()
}

// JSON renders the report as indented JSON.
func (r *StatusReport) JSON() ([]byte, error) {
	return json.Marshal(r)
}

// ingestDetail assembles the detail column of the ingest row for a
// readable path (live or away). sinceLabel/since render how long the
// current source has been feeding the path ("up" live, "away" offline).
func ingestDetail(i IngestStatus, expectedReaders int, sinceLabel string, since *time.Time) string {
	var parts []string
	if i.Kbps > 0 {
		parts = append(parts, fmtKbps(i.Kbps))
	}
	if i.Resolution != "" {
		res := i.Resolution
		if i.VideoCodec != "" {
			res += " " + strings.ToLower(i.VideoCodec)
		}
		parts = append(parts, res)
	}
	if i.AudioCodec != "" {
		parts = append(parts, strings.ToLower(i.AudioCodec))
	}
	parts = append(parts, fmt.Sprintf("readers %d/%d", i.Readers, expectedReaders))
	if since != nil {
		parts = append(parts, sinceLabel+" "+humanDuration(time.Since(*since)))
	}
	return strings.Join(parts, "  ")
}

func platformDetail(p PlatformStatus) string {
	if p.Running {
		if p.ConnErr != "" {
			return fmt.Sprintf("running, connection check failed (%s), restarts %d", p.ConnErr, p.Restarts)
		}
		if !p.Connected {
			return fmt.Sprintf("running, not connected to relay, restarts %d", p.Restarts)
		}
		d := fmt.Sprintf("connected, restarts %d", p.Restarts)
		if p.Since != nil {
			d += ", up " + humanDuration(time.Since(*p.Since))
		}
		return d
	}
	if p.State != "" {
		d := fmt.Sprintf("%s, restarts %d", p.State, p.Restarts)
		if p.LastError != "" {
			d += ", " + p.LastError
		}
		return d
	}
	if !p.Managed {
		return "not running (daemon not running)"
	}
	return "not running"
}

func fmtKbps(kbps int) string {
	if kbps >= 1000 {
		return fmt.Sprintf("%.2f Mbps", float64(kbps)/1000)
	}
	return fmt.Sprintf("%d kbps", kbps)
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

const (
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cRed    = "\x1b[31m"
	cReset  = "\x1b[0m"
)

// ColorEnabled reports whether ANSI colors should be used on stdout:
// enabled unless --no-color, $NO_COLOR is set, or stdout is not a TTY.
func ColorEnabled(noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
