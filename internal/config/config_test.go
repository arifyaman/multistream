package config

import (
	"os"
	"path/filepath"
	"testing"
)

const validConfig = `{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/test",
  "platforms": [
    {"name": "twitch", "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}"}
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
	if cfg.FFmpegPath != "ffmpeg" {
		t.Errorf("default ffmpeg_path = %q, want ffmpeg", cfg.FFmpegPath)
	}
	if cfg.RestartSec != 5 || cfg.StartLimitIntervalSec != 60 || cfg.StartLimitBurst != 5 {
		t.Errorf("supervisor defaults = %d/%d/%d, want 5/60/5",
			cfg.RestartSec, cfg.StartLimitIntervalSec, cfg.StartLimitBurst)
	}
	if got, want := cfg.InputURL(), "rtmp://127.0.0.1:1935/live/test"; got != want {
		t.Errorf("InputURL = %q, want %q", got, want)
	}
	if cfg.Source() != path {
		t.Errorf("source = %q, want %q", cfg.Source(), path)
	}
	p, ok := cfg.PlatformByName("twitch")
	if !ok {
		t.Fatal("twitch platform not found")
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
		"away_file": "/etc/multistream/away.mp4",
		"keys_dir": "/etc/multistream/keys",
		"ffmpeg_path": "/opt/ffmpeg/bin/ffmpeg",
		"platforms": [
			{"name": "a", "push_url": "rtmp://h/p"}
		]
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IngestPort != 9999 || cfg.RefreshSec != 5 {
		t.Errorf("explicit values not preserved: %+v", cfg)
	}
	if cfg.FFmpegPath != "/opt/ffmpeg/bin/ffmpeg" {
		t.Errorf("ffmpeg_path = %q, want explicit value", cfg.FFmpegPath)
	}
	if got := cfg.KeyFile(&cfg.Platforms[0]); got != "/etc/multistream/keys/a.env" {
		t.Errorf("KeyFile = %q", got)
	}
	if cfg.AwayFile != "/etc/multistream/away.mp4" {
		t.Errorf("away_file = %q", cfg.AwayFile)
	}
}

func TestLoadConfigAwayFileOptional(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AwayFile != "" {
		t.Errorf("away_file = %q, want empty when unset", cfg.AwayFile)
	}
}

func TestDefaultConfigPaths(t *testing.T) {
	t.Setenv("MULTISTREAM_CONFIG", "/custom/path.json")
	t.Setenv("HOME", t.TempDir()) // make os.UserConfigDir deterministic
	os.Unsetenv("XDG_CONFIG_HOME")
	paths := DefaultConfigPaths()
	if paths[0] != "/custom/path.json" {
		t.Errorf("env override not first: %v", paths)
	}
	// env, per-user config dir, system-wide, and ./config.json.
	if len(paths) != 4 {
		t.Errorf("want 4 candidates, got %v", paths)
	}
	if paths[len(paths)-2] != "/etc/multistream/config.json" || paths[len(paths)-1] != "config.json" {
		t.Errorf("system and local paths missing: %v", paths)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := map[string]string{
		"empty object":          `{}`,
		"bad api scheme":        `{"mediamtx_api":"ftp://x","ingest_path":"a","platforms":[{"name":"a","push_url":"rtmp://h/p"}]}`,
		"api without host":      `{"mediamtx_api":"http://","ingest_path":"a","platforms":[{"name":"a","push_url":"rtmp://h/p"}]}`,
		"missing ingest path":   `{"mediamtx_api":"http://127.0.0.1:9997","platforms":[{"name":"a","push_url":"rtmp://h/p"}]}`,
		"no platforms":          `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[]}`,
		"missing platform name": `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"push_url":"rtmp://h/p"}]}`,
		"missing push url":      `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"name":"a"}]}`,
		"duplicate names":       `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","platforms":[{"name":"a","push_url":"rtmp://h/p"},{"name":"a","push_url":"rtmp://h/p"}]}`,
		"relative away file":    `{"mediamtx_api":"http://127.0.0.1:9997","ingest_path":"a","away_file":"away.mp4","platforms":[{"name":"a","push_url":"rtmp://h/p"}]}`,
		"invalid json":          `{not json`,
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
