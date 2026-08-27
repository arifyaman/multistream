package mediamtx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// writeJSON writes a canned API response, recording errors on t.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	if status != 0 {
		w.WriteHeader(status)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

const pathBody = `{
  "name": "live/test",
  "online": true,
  "onlineTime": "2026-08-27T21:14:02Z",
  "inboundBytes": 123456789,
  "outboundBytes": 987654321,
  "readers": [
    {"id": "a", "type": "rtmp"},
    {"id": "b", "type": "rtmp"},
    {"id": "c", "type": "rtmp"}
  ],
  "tracks2": [
    {"codec": "H264", "codecProps": {"width": 1920, "height": 1080, "profile": "High", "level": "4.2"}},
    {"codec": "MPEG-4 Audio", "codecProps": {"channelCount": 2, "sampleRate": 48000}}
  ]
}`

func TestGetPathInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/paths/get/live/test" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(t, w, 0, pathBody)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	p, err := c.GetPath(context.Background(), "live/test")
	if err != nil {
		t.Fatal(err)
	}
	info := p.Info()
	if !info.Online {
		t.Error("want online true")
	}
	if info.Readers != 3 {
		t.Errorf("readers = %d, want 3", info.Readers)
	}
	if info.InboundBytes != 123456789 {
		t.Errorf("inboundBytes = %d", info.InboundBytes)
	}
	if info.Resolution != "1920x1080" {
		t.Errorf("resolution = %q, want 1920x1080", info.Resolution)
	}
	if info.VideoCodec != "H264" {
		t.Errorf("videoCodec = %q, want H264", info.VideoCodec)
	}
	if info.AudioCodec != "MPEG-4 Audio" {
		t.Errorf("audioCodec = %q, want MPEG-4 Audio", info.AudioCodec)
	}
	want := time.Date(2026, 8, 27, 21, 14, 2, 0, time.UTC)
	if !info.OnlineSince.Equal(want) {
		t.Errorf("onlineSince = %v, want %v", info.OnlineSince, want)
	}
}

func TestGetPathNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, `{"error":"path not found","status":404}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.GetPath(context.Background(), "live/missing"); err == nil {
		t.Fatal("want error for 404")
	}
}

func TestGetPathUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	if _, err := c.GetPath(context.Background(), "live/test"); err == nil {
		t.Fatal("want error for unreachable API")
	}
}

func TestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/info" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		writeJSON(t, w, 0, `{"started":"2026-08-27T20:00:00Z","version":"v1.20.1"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != "v1.20.1" {
		t.Errorf("version = %q, want v1.20.1", v)
	}
}

func TestInfoNoVideoTrack(t *testing.T) {
	body := `{
	  "name": "live/audio-only",
	  "online": true,
	  "readers": [],
	  "tracks2": [
	    {"codec": "MPEG-4 Audio", "codecProps": {"channelCount": 2, "sampleRate": 48000}}
	  ]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 0, body)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	p, err := c.GetPath(context.Background(), "live/audio-only")
	if err != nil {
		t.Fatal(err)
	}
	info := p.Info()
	if info.Resolution != "" {
		t.Errorf("resolution = %q, want empty", info.Resolution)
	}
	if info.VideoCodec != "" {
		t.Errorf("videoCodec = %q, want empty", info.VideoCodec)
	}
	if info.AudioCodec != "MPEG-4 Audio" {
		t.Errorf("audioCodec = %q", info.AudioCodec)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short) = %q", got)
	}
	if got := truncate("0123456789", 5); got != "01..." {
		t.Errorf("truncate(long) = %q", got)
	}
}
