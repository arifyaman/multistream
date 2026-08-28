package check

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePushURL(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{"rtmp://eun10.contribute.live-video.net/app/KEY", "eun10.contribute.live-video.net", 1935},
		{"rtmps://kick-cdn.example.com/KEY", "kick-cdn.example.com", 443},
		{"rtmps://kick-cdn.example.com//KEY", "kick-cdn.example.com", 443},
		{"rtmp://a.rtmp.youtube.com:1935/live2/NAME", "a.rtmp.youtube.com", 1935},
		{"rtmp://live.kick.com:9443/KEY", "live.kick.com", 9443},
		{"http://example.com", "example.com", 1935},
		{"", "", 0},
		{"not a url", "", 0},
	}
	for _, c := range cases {
		host, port := parsePushURL(c.in)
		if host != c.wantHost || port != c.wantPort {
			t.Errorf("parsePushURL(%q) = (%q, %d), want (%q, %d)",
				c.in, host, port, c.wantHost, c.wantPort)
		}
	}
}

func writeAwayFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "away.mp4")
	if err := os.WriteFile(p, content, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckAwayFile(t *testing.T) {
	cases := []struct {
		name        string
		size        int
		wantOK      bool
		mtxVersion  string
		haveVersion bool
	}{
		{"missing file", 0, false, "v1.20.1", true},
		{"empty file", 0, false, "v1.20.1", true},
		{"ok new mediamtx", 1024, true, "v1.20.1", true},
		{"ok old mediamtx", 1024, false, "v1.16.2", true},
		{"ok at version boundary", 1024, true, "v1.16.3", true},
		{"version unknown", 1024, true, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var path string
			if c.name == "missing file" {
				path = "/nonexistent/away.mp4"
			} else {
				path = writeAwayFile(t, make([]byte, c.size))
			}
			got := checkAwayFile(path, c.mtxVersion, c.haveVersion)
			if got != c.wantOK {
				t.Errorf("checkAwayFile = %v, want %v", got, c.wantOK)
			}
		})
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{2 * 1024 * 1024, "2.0 MiB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
