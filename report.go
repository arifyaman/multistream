package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const sampleInterval = time.Second

// IngestStatus is the normalized state of the OBS -> mediamtx link.
type IngestStatus struct {
	APIError    string     `json:"api_error,omitempty"`
	Online      bool       `json:"online"`
	Kbps        int        `json:"kbps,omitempty"`
	Bytes       uint64     `json:"bytes_received,omitempty"`
	Readers     int        `json:"readers"`
	Resolution  string     `json:"resolution,omitempty"`
	VideoCodec  string     `json:"video_codec,omitempty"`
	AudioCodec  string     `json:"audio_codec,omitempty"`
	OnlineSince *time.Time `json:"online_since,omitempty"`
}

// PlatformStatus is the normalized state of one re-broadcaster.
type PlatformStatus struct {
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	UnitExists bool   `json:"unit_exists"`
	UnitError  string `json:"unit_error,omitempty"`
	Active     string `json:"active_state,omitempty"`
	Sub        string `json:"sub_state,omitempty"`
	Connected  bool   `json:"connected"`
	ConnErr    string `json:"connected_error,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Restarts   int    `json:"restarts,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// Good reports whether the platform is fully healthy: unit running and
// actually pulling from mediamtx.
func (p PlatformStatus) Good() bool {
	return p.UnitExists && p.UnitError == "" &&
		p.Active == "active" && p.Sub == "running" && p.Connected && p.ConnErr == ""
}

// StatusReport is the full picture the CLI prints.
type StatusReport struct {
	Time            time.Time        `json:"time"`
	Ingest          IngestStatus     `json:"ingest"`
	Platforms       []PlatformStatus `json:"platforms"`
	ExpectedReaders int              `json:"expected_readers"`
	OK              bool             `json:"ok"`
}

// Collector keeps sampling state between Collect calls (bitrate deltas).
type Collector struct {
	cfg       *Config
	client    *mmtxClient
	lastBytes uint64
	lastTime  time.Time
}

// NewCollector builds a Collector for the given config.
func NewCollector(cfg *Config) *Collector {
	return &Collector{cfg: cfg, client: newMediaMTXClient(cfg.MediaMTXAPI)}
}

// Collect takes one snapshot of the whole chain.
func (c *Collector) Collect() (*StatusReport, error) {
	rep := &StatusReport{Time: time.Now(), ExpectedReaders: len(c.cfg.Platforms)}
	c.collectIngest(rep)
	for _, p := range c.cfg.Platforms {
		rep.Platforms = append(rep.Platforms, c.collectPlatform(p))
	}
	rep.OK = rep.Ingest.APIError == "" && rep.Ingest.Online &&
		rep.Ingest.Readers >= rep.ExpectedReaders
	for _, ps := range rep.Platforms {
		if !ps.Good() {
			rep.OK = false
		}
	}
	return rep, nil
}

func (c *Collector) collectIngest(rep *StatusReport) {
	path, err := c.client.GetPath(c.cfg.IngestPath)
	if err != nil {
		rep.Ingest.APIError = err.Error()
		return
	}
	info := path.Info()
	rep.Ingest.Online = info.Online
	rep.Ingest.Readers = info.Readers
	rep.Ingest.Resolution = info.Resolution
	rep.Ingest.VideoCodec = info.VideoCodec
	rep.Ingest.AudioCodec = info.AudioCodec
	rep.Ingest.Bytes = info.InboundBytes
	if !info.OnlineSince.IsZero() {
		ts := info.OnlineSince
		rep.Ingest.OnlineSince = &ts
	}

	// Bitrate = delta of inbound bytes. First call samples twice so a
	// one-shot `status` still reports a number; later calls (watch mode)
	// reuse the previous sample.
	now := time.Now()
	if c.lastTime.IsZero() {
		time.Sleep(sampleInterval)
		if path2, err2 := c.client.GetPath(c.cfg.IngestPath); err2 == nil {
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

func (c *Collector) collectPlatform(p Platform) PlatformStatus {
	ps := PlatformStatus{Name: p.Name, Unit: p.Unit}
	st, err := QueryUnit(p.Unit)
	if err != nil {
		ps.UnitError = err.Error()
		return ps
	}
	ps.UnitExists = st.Exists
	ps.Active = st.Active
	ps.Sub = st.Sub
	ps.Restarts = st.Restarts
	ps.PID = st.PID
	if st.Exists && st.Active == "active" && st.Sub == "running" {
		ok, err := PIDConnectedTo(st.PID, "127.0.0.1", c.cfg.IngestPort)
		if err != nil {
			ps.ConnErr = err.Error()
		} else {
			ps.Connected = ok
		}
	}
	if st.Exists && !(st.Active == "active" && st.Sub == "running") {
		ps.LastError = LastUnitError(p.Unit)
	}
	return ps
}

// Events returns human-readable transition lines between prev and the
// current report (for watch mode).
func (r *StatusReport) Events(prev *StatusReport) []string {
	if prev == nil {
		return nil
	}
	var out []string
	now := time.Now().Format("15:04:05")
	if prev.Ingest.Online != r.Ingest.Online {
		if r.Ingest.Online {
			out = append(out, fmt.Sprintf("%s ingest PUBLISHING", now))
		} else {
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
	case !p.UnitExists:
		return "unit not found"
	case p.UnitError != "":
		return "unit query failed"
	case p.Active == "failed":
		return "failed"
	case p.Active != "active" || p.Sub != "running":
		return p.Active + "/" + p.Sub
	default:
		return "not connected"
	}
}

// renderTable renders the report as a plain text table.
func renderTable(r *StatusReport, color bool) string {
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
		case "DOWN":
			return cRed + s + cReset
		}
		return s
	}
	row := func(name, st, detail string) {
		fmt.Fprintf(&b, "%-*s  %s  %s\n", width, name, state(st), detail)
	}

	i := r.Ingest
	ingSt, ingDetail := "UP", ""
	switch {
	case i.APIError != "":
		ingSt, ingDetail = "DOWN", "api error: "+i.APIError
	case !i.Online:
		ingSt, ingDetail = "DOWN", "no publisher"
	default:
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
		parts = append(parts, fmt.Sprintf("readers %d/%d", i.Readers, r.ExpectedReaders))
		if i.OnlineSince != nil {
			parts = append(parts, "up "+humanDuration(time.Since(*i.OnlineSince)))
		}
		ingDetail = strings.Join(parts, "  ")
	}
	row("ingest", ingSt, ingDetail)

	for _, p := range r.Platforms {
		st := "DOWN"
		if p.Good() {
			st = "UP"
		}
		row(p.Name, st, platformDetail(p))
	}
	return b.String()
}

func platformDetail(p PlatformStatus) string {
	if !p.UnitExists {
		return "unit not found"
	}
	if p.UnitError != "" {
		return "unit query failed: " + p.UnitError
	}
	if p.Active == "active" && p.Sub == "running" {
		if p.ConnErr != "" {
			return fmt.Sprintf("running, connected check failed (%s), restarts %d", p.ConnErr, p.Restarts)
		}
		if !p.Connected {
			return fmt.Sprintf("running, not connected to mediamtx, restarts %d", p.Restarts)
		}
		return fmt.Sprintf("connected, restarts %d", p.Restarts)
	}
	detail := fmt.Sprintf("%s/%s, restarts %d", p.Active, p.Sub, p.Restarts)
	if p.LastError != "" {
		detail += ", " + p.LastError
	}
	return detail
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
	cGreen = "\x1b[32m"
	cRed   = "\x1b[31m"
	cReset = "\x1b[0m"
)

// colorEnabled reports whether ANSI colors should be used on stdout.
func colorEnabled(noColorFlag bool) bool {
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

func printJSON(r *StatusReport) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(r)
}

func printJSONLine(r *StatusReport) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}
