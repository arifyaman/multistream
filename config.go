package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Platform is one re-broadcast destination.
type Platform struct {
	Name    string `json:"name"`
	Unit    string `json:"unit"`
	PushURL string `json:"push_url"`
}

// Config is the CLI configuration, loaded from a JSON file.
type Config struct {
	MediaMTXAPI string     `json:"mediamtx_api"`
	IngestPath  string     `json:"ingest_path"`
	IngestPort  int        `json:"ingest_port,omitempty"`
	RefreshSec  int        `json:"refresh_sec,omitempty"`
	KeysDir     string     `json:"keys_dir,omitempty"`
	Platforms   []Platform `json:"platforms"`

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

func defaultConfigPaths() []string {
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
		for _, p := range defaultConfigPaths() {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return nil, fmt.Errorf("config not found (searched %s); use -config <file> or $MULTISTREAM_CONFIG",
				strings.Join(defaultConfigPaths(), ", "))
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

// KeyFile returns the expected path of a platform's key env file, or "".
func (c *Config) KeyFile(p *Platform) string {
	if c.KeysDir == "" {
		return ""
	}
	return c.KeysDir + "/" + p.Name + ".env"
}
