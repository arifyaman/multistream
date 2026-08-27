package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const apiTimeout = 3 * time.Second

// mmtxClient talks to the mediamtx Control API.
type mmtxClient struct {
	base string
	http *http.Client
}

func newMediaMTXClient(base string) *mmtxClient {
	return &mmtxClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: apiTimeout},
	}
}

// mmtxPath mirrors the mediamtx v3 API Path object (fields we use).
type mmtxPath struct {
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
func (p *mmtxPath) Info() IngestInfo {
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

func (c *mmtxClient) get(path string, v interface{}) error {
	resp, err := c.http.Get(c.base + path)
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

// GetPath fetches a single path. Returns an error on 404 (not published).
func (c *mmtxClient) GetPath(name string) (*mmtxPath, error) {
	var p mmtxPath
	if err := c.get("/v3/paths/get/"+url.PathEscape(name), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Version returns the mediamtx version string (for `check`).
func (c *mmtxClient) Version() (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.get("/v3/info", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
