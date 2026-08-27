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
type Path struct {
	Name          string `json:"name"`
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
	Online       bool
	OnlineSince  time.Time
	InboundBytes uint64
	Readers      int
	Resolution   string
	VideoCodec   string
	AudioCodec   string
}

// Info normalizes a path into the report's ingest fields.
func (p *Path) Info() IngestInfo {
	info := IngestInfo{
		Online:       p.Online,
		InboundBytes: p.InboundBytes,
		Readers:      len(p.Readers),
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
