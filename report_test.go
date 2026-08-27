package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func healthyPlatform(name string) PlatformStatus {
	return PlatformStatus{Name: name, Unit: "multistream-" + name, UnitExists: true,
		Active: "active", Sub: "running", Connected: true, PID: 1000}
}

func TestStatusReportJSON(t *testing.T) {
	since := time.Now().Add(-time.Hour)
	r := &StatusReport{Time: time.Now(), ExpectedReaders: 2, OK: true}
	r.Ingest = IngestStatus{Online: true, Kbps: 6000, Readers: 2,
		Resolution: "1920x1080", VideoCodec: "H264", AudioCodec: "MPEG-4 Audio", OnlineSince: &since}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch"), healthyPlatform("kick")}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"ok":true`, `"twitch"`, `"connected":true`, `"readers":2`, `"kbps":6000`} {
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
	cur.Platforms = []PlatformStatus{{Name: "twitch", UnitExists: true, Active: "failed", Sub: "failed", LastError: "connection refused"}}

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

func TestRenderTableAllUp(t *testing.T) {
	since := time.Now().Add(-114 * time.Minute)
	r := &StatusReport{Time: time.Now(), ExpectedReaders: 2, OK: true}
	r.Ingest = IngestStatus{Online: true, Kbps: 6020, Readers: 2,
		Resolution: "1920x1080", VideoCodec: "H264", AudioCodec: "MPEG-4 Audio", OnlineSince: &since}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch"), healthyPlatform("kick")}

	out := renderTable(r, false)
	for _, want := range []string{
		"ingest", "6.02 Mbps", "1920x1080 h264", "mpeg-4 audio", "readers 2/2",
		"up 1h54m", "twitch", "connected, restarts 0", "kick",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTableDegraded(t *testing.T) {
	r := &StatusReport{Time: time.Now(), ExpectedReaders: 1, OK: false}
	r.Ingest = IngestStatus{Online: false}
	r.Platforms = []PlatformStatus{
		{Name: "twitch", UnitExists: true, Active: "failed", Sub: "failed", Restarts: 3, LastError: "connection refused"},
		{Name: "kick", UnitExists: false},
		{Name: "youtube", UnitExists: true, Active: "active", Sub: "running", PID: 5},
	}

	out := renderTable(r, false)
	for _, want := range []string{
		"no publisher",
		"failed/failed, restarts 3, connection refused",
		"unit not found",
		"running, not connected to mediamtx, restarts 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTableAPIError(t *testing.T) {
	r := &StatusReport{Time: time.Now(), ExpectedReaders: 1, OK: false}
	r.Ingest = IngestStatus{APIError: "mediamtx API /v3/paths/get/live/x: HTTP 500"}
	r.Platforms = []PlatformStatus{healthyPlatform("twitch")}

	out := renderTable(r, false)
	if !strings.Contains(out, "api error:") {
		t.Errorf("table missing api error:\n%s", out)
	}
}

func TestDownReason(t *testing.T) {
	cases := []struct {
		in   PlatformStatus
		want string
	}{
		{PlatformStatus{UnitExists: false}, "unit not found"},
		{PlatformStatus{UnitExists: true, UnitError: "x"}, "unit query failed"},
		{PlatformStatus{UnitExists: true, Active: "failed"}, "failed"},
		{PlatformStatus{UnitExists: true, Active: "inactive", Sub: "dead"}, "inactive/dead"},
		{PlatformStatus{UnitExists: true, Active: "active", Sub: "running"}, "not connected"},
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
	p.Sub = "auto-restart"
	if p.Good() {
		t.Error("restarting platform should not be Good")
	}
}
