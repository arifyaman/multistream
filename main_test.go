package main

import (
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
