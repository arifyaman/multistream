// Package config loads and validates the multistream configuration file.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Platform is one re-broadcast destination.
type Platform struct {
	// Name is the unique, user-facing identifier (also the key file name
	// stem: <keys_dir>/<name>.env).
	Name string `json:"name"`
	// PushURL is the RTMP(S) push URL. It may contain ${ENV_VAR} templates
	// that the daemon resolves from the platform's key file before spawning
	// ffmpeg; the read-only commands never resolve them.
	PushURL string `json:"push_url"`
}

// Config is the CLI configuration, loaded from a JSON file.
type Config struct {
	// MediaMTXAPI is the mediamtx Control API base URL (loopback).
	MediaMTXAPI string `json:"mediamtx_api"`
	// IngestPath is the mediamtx path OBS publishes to (e.g. "live/<name>").
	IngestPath string `json:"ingest_path"`
	// IngestPort is the mediamtx RTMP port used for per-platform
	// connection checks. Default 1935.
	IngestPort int `json:"ingest_port,omitempty"`
	// RefreshSec is the default --watch refresh interval in seconds.
	// Default 2.
	RefreshSec int `json:"refresh_sec,omitempty"`
	// KeysDir holds the 0600 key env files (<name>.env per platform).
	KeysDir string `json:"keys_dir,omitempty"`
	// AwayFile is the MP4 that mediamtx loops on the ingest path while no
	// publisher is connected (the mediamtx "always available" feature,
	// requires mediamtx >= 1.16.3 and alwaysAvailable in its config).
	// Optional; when set, `check` verifies the file exists.
	AwayFile string `json:"away_file,omitempty"`
	// FFmpegPath is the ffmpeg binary the daemon spawns. Default "ffmpeg"
	// (found on PATH). Set this when ffmpeg is not on PATH (common on
	// Windows).
	FFmpegPath string `json:"ffmpeg_path,omitempty"`
	// RestartSec is how long the daemon waits after an ffmpeg exit before
	// respawning it. Default 5.
	RestartSec int `json:"restart_sec,omitempty"`
	// StartLimitIntervalSec and StartLimitBurst bound restarts: if a
	// platform restarts more than StartLimitBurst times within
	// StartLimitIntervalSec, the daemon stops respawning it and marks it
	// "failed" (a manual `multistream restart` resets the limit). Defaults
	// are 60 and 5.
	StartLimitIntervalSec int `json:"start_limit_interval_sec,omitempty"`
	StartLimitBurst       int `json:"start_limit_burst,omitempty"`
	// Platforms is the list of re-broadcast destinations.
	Platforms []Platform `json:"platforms"`

	src string
}

// Source returns the file path the config was loaded from.
func (c *Config) Source() string { return c.src }

// PlatformByName looks up a platform by name.
func (c *Config) PlatformByName(name string) (*Platform, bool) {
	for i := range c.Platforms {
		if c.Platforms[i].Name == name {
			return &c.Platforms[i], true
		}
	}
	return nil, false
}

// KeyFile returns the expected path of a platform's key env file, or "".
func (c *Config) KeyFile(p *Platform) string {
	if c.KeysDir == "" {
		return ""
	}
	return c.KeysDir + "/" + p.Name + ".env"
}

// InputURL is the RTMP URL ffmpeg pulls from: the relay on loopback. The
// daemon uses it as the ffmpeg input and the read-only commands use it as
// the distinctive marker that identifies one of our ffmpeg processes in the
// process table.
func (c *Config) InputURL() string {
	return "rtmp://127.0.0.1:" + strconv.Itoa(c.IngestPort) + "/" + c.IngestPath
}

// DefaultConfigPaths returns the candidate config locations, in priority
// order: $MULTISTREAM_CONFIG, the per-user config dir, the system-wide
// /etc/multistream/config.json, and ./config.json.
func DefaultConfigPaths() []string {
	paths := []string{}
	if p := os.Getenv("MULTISTREAM_CONFIG"); p != "" {
		paths = append(paths, p)
	}
	if d, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(d, "multistream", "config.json"))
	}
	paths = append(paths, "/etc/multistream/config.json", "config.json")
	return paths
}

// LoadConfig reads and validates the config from path, or from the first
// default location that exists when path is empty.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		for _, p := range DefaultConfigPaths() {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return nil, fmt.Errorf("config not found (searched %s); use -config <file> or $MULTISTREAM_CONFIG",
				strings.Join(DefaultConfigPaths(), ", "))
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.src = path
	return &c, nil
}

func (c *Config) validate() error {
	u, err := url.Parse(c.MediaMTXAPI)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("mediamtx_api must be a http(s) URL, got %q", c.MediaMTXAPI)
	}
	if c.IngestPath == "" {
		return fmt.Errorf("ingest_path is required")
	}
	if c.IngestPort == 0 {
		c.IngestPort = 1935
	}
	if c.RefreshSec == 0 {
		c.RefreshSec = 2
	}
	if c.FFmpegPath == "" {
		c.FFmpegPath = "ffmpeg"
	}
	if c.RestartSec == 0 {
		c.RestartSec = 5
	}
	if c.StartLimitIntervalSec == 0 {
		c.StartLimitIntervalSec = 60
	}
	if c.StartLimitBurst == 0 {
		c.StartLimitBurst = 5
	}
	if c.AwayFile != "" && !filepath.IsAbs(c.AwayFile) {
		return fmt.Errorf("away_file must be an absolute path, got %q", c.AwayFile)
	}
	if len(c.Platforms) == 0 {
		return fmt.Errorf("at least one platform is required")
	}
	seen := map[string]bool{}
	for i, p := range c.Platforms {
		if p.Name == "" {
			return fmt.Errorf("platforms[%d]: name is required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("platforms[%d]: duplicate name %q", i, p.Name)
		}
		seen[p.Name] = true
		if p.PushURL == "" {
			return fmt.Errorf("platform %q: push_url is required", p.Name)
		}
	}
	return nil
}
