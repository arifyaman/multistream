package report

import (
	"strings"
	"testing"
	"time"
)

func healthyPlatform(name string) PlatformStatus {
	return PlatformStatus{Name: name, Managed: true, Running: true,
		Connected: true, PID: 1000, State: "running"}
}

func TestStatusReportJSON(t *testing.T) {
	since := time.Now().Add(-time.Hour)
	r := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 2, OK: true}
	r.Ingest = IngestStatus{Online: true, Available: true, Kbps: 6000, Readers: 2,
		Resolution: "1920x1080", VideoCodec: "H264", AudioCodec: "MPEG-4 Audio", OnlineSince: &since}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch"), healthyPlatform("kick")}

	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"ok":true`, `"daemon_up":true`, `"twitch"`, `"connected":true`,
		`"running":true`, `"readers":2`, `"kbps":6000`, `"online":true`, `"available":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("json missing %s: %s", want, s)
		}
	}
}

func TestStatusReportEvents(t *testing.T) {
	prev := &StatusReport{}
	prev.Ingest.Online = true
	prev.Platforms = []PlatformStatus{healthyPlatform("twitch")}

	cur := &StatusReport{}
	cur.Ingest.Online = false
	cur.Platforms = []PlatformStatus{{Name: "twitch", Managed: true, State: "failed", Restarts: 3, LastError: "connection refused"}}

	ev := cur.Events(prev)
	joined := strings.Join(ev, " | ")
	if !strings.Contains(joined, "ingest DROPPED") {
		t.Errorf("want ingest DROPPED event, got %q", joined)
	}
	if !strings.Contains(joined, "twitch DOWN") || !strings.Contains(joined, "failed") {
		t.Errorf("want twitch DOWN (failed) event, got %q", joined)
	}

	// No change => no events.
	if e := cur.Events(cur); len(e) != 0 {
		t.Errorf("want no events for identical reports, got %v", e)
	}
	// Nil prev => no events, no panic.
	if e := cur.Events(nil); e != nil {
		t.Errorf("want nil events for nil prev, got %v", e)
	}
}

func TestEventsAwayTransitions(t *testing.T) {
	ingest := func(state string) IngestStatus {
		switch state {
		case "live":
			return IngestStatus{Online: true, Available: true}
		case "away":
			return IngestStatus{Available: true}
		default:
			return IngestStatus{}
		}
	}
	cases := []struct {
		from, to string
		want     string
	}{
		{"live", "away", "ingest AWAY (offline segment, waiting for publisher)"},
		{"away", "live", "ingest PUBLISHING"},
		{"away", "down", "ingest DROPPED"},
		{"down", "away", "ingest AWAY (offline segment, waiting for publisher)"},
	}
	for _, c := range cases {
		prev := &StatusReport{Ingest: ingest(c.from)}
		cur := &StatusReport{Ingest: ingest(c.to)}
		joined := strings.Join(cur.Events(prev), " | ")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%s -> %s: want %q, got %q", c.from, c.to, c.want, joined)
		}
	}
	// Same state => no events.
	if e := ingest("away").State(); e != "away" {
		t.Fatalf("test setup: want away state, got %q", e)
	}
	prev := &StatusReport{Ingest: ingest("away")}
	if ev := (&StatusReport{Ingest: ingest("away")}).Events(prev); len(ev) != 0 {
		t.Errorf("want no away->away events, got %v", ev)
	}
}

func TestRenderAllUp(t *testing.T) {
	since := time.Now().Add(-114 * time.Minute)
	r := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 2, OK: true}
	r.Ingest = IngestStatus{Online: true, Kbps: 6020, Readers: 2,
		Resolution: "1920x1080", VideoCodec: "H264", AudioCodec: "MPEG-4 Audio", OnlineSince: &since}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch"), healthyPlatform("kick")}

	out := r.Render(false)
	for _, want := range []string{
		"ingest", "6.02 Mbps", "1920x1080 h264", "mpeg-4 audio", "readers 2/2",
		"up 1h54m", "twitch", "connected, restarts 0", "kick",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not running") {
		t.Errorf("all-up table must not mention the daemon being down:\n%s", out)
	}
}

func TestRenderDegraded(t *testing.T) {
	r := &StatusReport{Time: time.Now(), ExpectedReaders: 1, OK: false}
	r.Ingest = IngestStatus{Online: false}
	r.Platforms = []PlatformStatus{
		{Name: "twitch", Managed: true, State: "failed", Restarts: 3, LastError: "connection refused"},
		{Name: "kick"},
		{Name: "youtube", Managed: true, Running: true, PID: 5, State: "running"},
	}

	out := r.Render(false)
	for _, want := range []string{
		"no publisher",
		"failed, restarts 3, connection refused",
		"not running (daemon not running)",
		"running, not connected to relay, restarts 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "daemon    not running") {
		t.Errorf("daemon-down table must note the daemon is not running:\n%s", out)
	}
}

func TestRenderAPIError(t *testing.T) {
	r := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 1, OK: false}
	r.Ingest = IngestStatus{APIError: "mediamtx API /v3/paths/get/live/x: HTTP 500"}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch")}

	out := r.Render(false)
	if !strings.Contains(out, "api error:") {
		t.Errorf("table missing api error:\n%s", out)
	}
}

func TestRenderColor(t *testing.T) {
	r := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 1, OK: true}
	r.Ingest = IngestStatus{Online: true, Readers: 1}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch")}

	plain := r.Render(false)
	colored := r.Render(true)
	if strings.Contains(plain, "\x1b[") {
		t.Error("plain render must not contain ANSI codes")
	}
	if !strings.Contains(colored, cGreen) || !strings.Contains(colored, cReset) {
		t.Error("colored render must contain green state")
	}

	away := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 1, OK: true}
	away.Ingest = IngestStatus{Available: true, Readers: 1}
	away.Platforms = []PlatformStatus{healthyPlatform("twitch")}
	if !strings.Contains(away.Render(true), cYellow) {
		t.Error("colored away render must contain yellow state")
	}
}

func TestIngestState(t *testing.T) {
	cases := []struct {
		name string
		in   IngestStatus
		want string
	}{
		{"api error", IngestStatus{APIError: "boom"}, "down"},
		{"live", IngestStatus{Online: true, Available: true}, "live"},
		{"away", IngestStatus{Available: true}, "away"},
		{"offline", IngestStatus{}, "down"},
	}
	for _, c := range cases {
		if got := c.in.State(); got != c.want {
			t.Errorf("%s: State() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRenderAway(t *testing.T) {
	since := time.Now().Add(-65 * time.Minute)
	r := &StatusReport{Time: time.Now(), DaemonUp: true, ExpectedReaders: 2, OK: true}
	r.Ingest = IngestStatus{Available: true, Kbps: 1200, Readers: 2,
		Resolution: "1920x1080", VideoCodec: "H264", AudioCodec: "MPEG-4 Audio", AvailableSince: &since}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch"), healthyPlatform("kick")}

	out := r.Render(false)
	for _, want := range []string{
		"AWAY", "1.20 Mbps", "1920x1080 h264", "readers 2/2", "away 1h05m",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("away table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "up ") {
		t.Errorf("away table must not show live uptime:\n%s", out)
	}
}

func TestDownReason(t *testing.T) {
	cases := []struct {
		in   PlatformStatus
		want string
	}{
		{PlatformStatus{Name: "a", Managed: false}, "daemon not running"},
		{PlatformStatus{Name: "a", Managed: true, Running: true, ConnErr: "x"}, "connection check failed"},
		{PlatformStatus{Name: "a", Managed: true, Running: true}, "not connected to relay"},
		{PlatformStatus{Name: "a", Managed: true, State: "failed", LastError: "connection refused"}, "failed, connection refused"},
		{PlatformStatus{Name: "a", Managed: true, State: "restarting"}, "restarting"},
		{PlatformStatus{Name: "a", Managed: true}, "not running"},
	}
	for _, c := range cases {
		if got := downReason(c.in); got != c.want {
			t.Errorf("downReason(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		15 * time.Second:              "15s",
		2*time.Minute + 5*time.Second: "2m05s",
		1*time.Hour + 54*time.Minute:  "1h54m",
		-5 * time.Second:              "0s",
	}
	for in, want := range cases {
		if got := humanDuration(in); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtKbps(t *testing.T) {
	if got := fmtKbps(500); got != "500 kbps" {
		t.Errorf("fmtKbps(500) = %q", got)
	}
	if got := fmtKbps(6020); got != "6.02 Mbps" {
		t.Errorf("fmtKbps(6020) = %q", got)
	}
}

func TestPlatformGood(t *testing.T) {
	if !healthyPlatform("twitch").Good() {
		t.Error("healthy platform should be Good")
	}
	p := healthyPlatform("twitch")
	p.Connected = false
	if p.Good() {
		t.Error("disconnected platform should not be Good")
	}
	p.Connected = true
	p.Running = false
	if p.Good() {
		t.Error("not-running platform should not be Good")
	}
}
