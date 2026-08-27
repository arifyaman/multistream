// Package config loads and validates the multistream configuration file.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Platform is one re-broadcast destination.
type Platform struct {
	// Name is the unique, user-facing identifier (also the key file name
	// stem: <keys_dir>/<name>.env).
	Name string `json:"name"`
	// Unit is the systemd unit that pushes to this platform.
	Unit string `json:"unit"`
	// PushURL is the RTMP(S) push URL. It may contain ${ENV_VAR} templates
	// (used by the systemd unit); the CLI itself never resolves them.
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

// DefaultConfigPaths returns the candidate config locations, in priority
// order: $MULTISTREAM_CONFIG, /etc/multistream/config.json, ./config.json.
func DefaultConfigPaths() []string {
	paths := []string{}
	if p := os.Getenv("MULTISTREAM_CONFIG"); p != "" {
		paths = append(paths, p)
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
		if p.Unit == "" {
			return fmt.Errorf("platform %q: unit is required", p.Name)
		}
		if p.PushURL == "" {
			return fmt.Errorf("platform %q: push_url is required", p.Name)
		}
	}
	return nil
}
