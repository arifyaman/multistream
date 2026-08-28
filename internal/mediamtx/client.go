// Package mediamtx is a minimal client for the mediamtx v1 Control API.
package mediamtx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultTimeout bounds every Control API request.
const defaultTimeout = 3 * time.Second

// Client talks to the mediamtx Control API.
type Client struct {
	base string
	http *http.Client
}

// NewClient builds a Client for the given API base URL
// (e.g. "http://127.0.0.1:9997").
func NewClient(base string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: defaultTimeout},
	}
}

// Path mirrors the mediamtx v1 API Path object (fields we use).
//
// Available (mediamtx >= 1.16.3) reports whether the path can be read,
// including when only the always-available offline segment is playing.
// Online reports whether a real publisher is connected.
type Path struct {
	Name          string `json:"name"`
	Available     bool   `json:"available"`
	AvailableTime string `json:"availableTime"`
	Online        bool   `json:"online"`
	OnlineTime    string `json:"onlineTime"`
	InboundBytes  uint64 `json:"inboundBytes"`
	OutboundBytes uint64 `json:"outboundBytes"`
	Readers       []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"readers"`
	Tracks []struct {
		Codec      string          `json:"codec"`
		CodecProps json.RawMessage `json:"codecProps"`
	} `json:"tracks2"`
}

// IngestInfo is the normalized view of the ingest path the report needs.
type IngestInfo struct {
	Online         bool
	Available      bool
	OnlineSince    time.Time
	AvailableSince time.Time
	InboundBytes   uint64
	Readers        int
	Resolution     string
	VideoCodec     string
	AudioCodec     string
}

// Info normalizes a path into the report's ingest fields.
func (p *Path) Info() IngestInfo {
	info := IngestInfo{
		// An online path is always readable, even on mediamtx versions
		// (< 1.16.3) that do not report the "available" field.
		Available:    p.Available || p.Online,
		Online:       p.Online,
		InboundBytes: p.InboundBytes,
		Readers:      len(p.Readers),
	}
	if p.AvailableTime != "" {
		if t, err := time.Parse(time.RFC3339, p.AvailableTime); err == nil {
			info.AvailableSince = t
		}
	}
	if p.OnlineTime != "" {
		if t, err := time.Parse(time.RFC3339, p.OnlineTime); err == nil {
			info.OnlineSince = t
		}
	}
	for _, t := range p.Tracks {
		var vp struct {
			Width  int64 `json:"width"`
			Height int64 `json:"height"`
		}
		if err := json.Unmarshal(t.CodecProps, &vp); err == nil && vp.Width > 0 && vp.Height > 0 {
			info.Resolution = strconv.FormatInt(vp.Width, 10) + "x" + strconv.FormatInt(vp.Height, 10)
			info.VideoCodec = t.Codec
			continue
		}
		if info.AudioCodec == "" {
			info.AudioCodec = t.Codec
		}
	}
	return info
}

// GetPath fetches a single path. Returns an error on 404 (not published).
func (c *Client) GetPath(ctx context.Context, name string) (*Path, error) {
	var p Path
	if err := c.get(ctx, "/v3/paths/get/"+url.PathEscape(name), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Version returns the mediamtx version string (for the check command).
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/v3/info", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// VersionAtLeast reports whether the mediamtx version string v
// (e.g. "v1.20.1") is at least min (e.g. "1.16.3").
func VersionAtLeast(v, min string) (bool, error) {
	cur, err := parseVersion(v)
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", v, err)
	}
	want, err := parseVersion(min)
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", min, err)
	}
	for i := 0; i < len(want); i++ {
		var have int
		if i < len(cur) {
			have = cur[i]
		}
		if have < want[i] {
			return false, nil
		}
		if have > want[i] {
			return true, nil
		}
	}
	return true, nil
}

// parseVersion parses a dotted numeric version, tolerating a leading "v"
// and trailing pre-release suffixes (e.g. "v1.16.3-15-gabc").
func parseVersion(s string) ([]int, error) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid segment %q", p)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty version")
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, path string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mediamtx API %s: HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 120))
	}
	return json.Unmarshal(body, v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
