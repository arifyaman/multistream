package main

import (
	"os"
	"path/filepath"
	"testing"
)

const validConfig = `{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/test",
  "platforms": [
    {"name": "twitch", "unit": "multistream-twitch", "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}"}
  ]
}`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigValid(t *testing.T) {
	path := writeConfig(t, validConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IngestPort != 1935 {
		t.Errorf("default ingest port = %d, want 1935", cfg.IngestPort)
	}
	if cfg.RefreshSec != 2 {
		t.Errorf("default refresh = %d, want 2", cfg.RefreshSec)
	}
	if cfg.Source() != path {
		t.Errorf("source = %q, want %q", cfg.Source(), path)
	}
	p, ok := cfg.PlatformByName("twitch")
	if !ok {
		t.Fatal("twitch platform not found")
	}
	if p.Unit != "multistream-twitch" {
		t.Errorf("unit = %q", p.Unit)
	}
	if _, ok := cfg.PlatformByName("nope"); ok {
		t.Error("unknown platform should not be found")
	}
	if got := cfg.KeyFile(p); got != "" {
		t.Errorf("KeyFile with no keys_dir = %q, want empty", got)
	}
}

func TestLoadConfigDefaultsApplied(t *testing.T) {
	path := writeConfig(t, `{
		"mediamtx_api": "http://127.0.0.1:9997",
		"ingest_path": "live/test",
		"ingest_port": 9999,
		"refresh_sec": 5,
		"keys_dir": "/etc/multistream/keys",
		"platforms": [
			{"name": "a", "unit": "u-a", "push_url": "rtmp://h/p"}
		]
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IngestPort != 9999 || cfg.RefreshSec != 5 {
		t.Errorf("explicit values not preserved: %+v", cfg)
	}
	if got := cfg.KeyFile(&cfg.Platforms[0]); got != "/etc/multistream/keys/a.env" {
		t.Errorf("KeyFile = %q", got)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := map[string]string{
		"empty object":            `{}`,
		"bad api scheme":          `{"mediamtx_api":"ftp://x","ingest_path":"a","platforms":[{"name":"a","unit":"u","push_url":"rtmp://h/p"}]}`,
		"api without host":        `{"mediamtx_api":"http://","ingest_path":"a","platforms":[{"name":"a","unit":"u","push_url":"rtmp://h/p"}]}`,
		"missing ingest path":     `{"mediamtx_api":"http://127.0.0.1:9997","platforms":[{"name":"a","unit":"u","push_url":"rtmp://h/p"}]}`,
		"no platforms":            `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[]}`,
		"missing platform name":   `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"unit":"u","push_url":"rtmp://h/p"}]}`,
		"missing platform unit":   `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"name":"a","push_url":"rtmp://h/p"}]}`,
		"missing push url":        `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"name":"a","unit":"u"}]}`,
		"duplicate platform name": `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"name":"a","unit":"u1","push_url":"rtmp://h/p"},{"name":"a","unit":"u2","push_url":"rtmp://h/p"}]}`,
		"invalid json":            `{not json`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, content)
			if _, err := LoadConfig(path); err == nil {
				t.Errorf("want error for %s", content)
			}
		})
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig("/nonexistent/config.json"); err == nil {
		t.Error("want error for missing file")
	}
}
