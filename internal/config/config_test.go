package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/test",
  "platforms": [
    {"name": "twitch", "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}"}
  ]
}`

const twoProfiles = `{
  "default_profile": "me",
  "profiles": {
    "me": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/me",
      "keys_dir": "/etc/multistream/keys/me",
      "platforms": [
        {"name": "twitch", "push_url": "rtmp://live.twitch.tv/app/${TWITCH_KEY}"}
      ]
    },
    "friend": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/friend",
      "keys_dir": "/etc/multistream/keys/friend",
      "platforms": [
        {"name": "youtube", "push_url": "rtmp://a.rtmp.youtube.com/live2/${YOUTUBE_STREAM_NAME}"}
      ]
    }
  }
}`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadProfile loads content and selects its profile with name ("" lets the
// normal default resolution run).
func loadProfile(t *testing.T, content, name string) *Config {
	t.Helper()
	file, err := LoadConfig(writeConfig(t, content))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := file.Select(name)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadConfigValid(t *testing.T) {
	cfg := loadProfile(t, validConfig, "")
	if cfg.Name != DefaultProfileName {
		t.Errorf("legacy file profile name = %q, want %q", cfg.Name, DefaultProfileName)
	}
	if cfg.IngestPort != 1935 {
		t.Errorf("default ingest port = %d, want 1935", cfg.IngestPort)
	}
	if cfg.RefreshSec != 2 {
		t.Errorf("default refresh = %d, want 2", cfg.RefreshSec)
	}
	if cfg.FFmpegPath != "" {
		t.Errorf("default ffmpeg_path = %q, want empty (resolved at runtime)", cfg.FFmpegPath)
	}
	if cfg.RestartSec != 5 || cfg.StartLimitIntervalSec != 60 || cfg.StartLimitBurst != 5 {
		t.Errorf("supervisor defaults = %d/%d/%d, want 5/60/5",
			cfg.RestartSec, cfg.StartLimitIntervalSec, cfg.StartLimitBurst)
	}
	if got, want := cfg.InputURL(), "rtmp://127.0.0.1:1935/live/test"; got != want {
		t.Errorf("InputURL = %q, want %q", got, want)
	}
	if cfg.Source() == "" {
		t.Errorf("source = %q, want the config path", cfg.Source())
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
	cfg := loadProfile(t, `{
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
	}`, "")
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
	cfg := loadProfile(t, validConfig, "")
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

func TestProfilesMapSelection(t *testing.T) {
	// Explicit name wins.
	cfg := loadProfile(t, twoProfiles, "friend")
	if cfg.Name != "friend" || cfg.IngestPath != "live/friend" {
		t.Errorf("selected = %q (ingest %q), want friend", cfg.Name, cfg.IngestPath)
	}
	// Without an explicit name, default_profile wins.
	cfg = loadProfile(t, twoProfiles, "")
	if cfg.Name != "me" {
		t.Errorf("default selection = %q, want me (default_profile)", cfg.Name)
	}
	// The MULTISTREAM_PROFILE env var is next in line.
	file, err := LoadConfig(writeConfig(t, twoProfiles))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvProfile, "friend")
	cfg, err = file.Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "friend" {
		t.Errorf("env selection = %q, want friend", cfg.Name)
	}
	// ...and an explicit name beats the env var.
	t.Setenv(EnvProfile, "friend")
	cfg, err = file.Select("me")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("explicit selection = %q, want me", cfg.Name)
	}
}

func TestProfilesMapFallbackWithoutDefaultProfile(t *testing.T) {
	content := `{
  "profiles": {
    "me": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/me",
      "platforms": [{"name": "twitch", "push_url": "rtmp://h/k"}]
    }
  }
}`
	file, err := LoadConfig(writeConfig(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Select(""); err == nil {
		t.Error("want error: no default_profile and no profile named default")
	} else if !strings.Contains(err.Error(), "-profile") {
		t.Errorf("error should hint at -profile: %v", err)
	}
	cfg, err := file.Select("me")
	if err != nil {
		t.Fatalf("explicit selection should work: %v", err)
	}
	if cfg.Name != "me" {
		t.Errorf("selected = %q, want me", cfg.Name)
	}
}

func TestActiveFileSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(twoProfiles), 0600); err != nil {
		t.Fatal(err)
	}
	mustLoad := func() *File {
		t.Helper()
		f, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	setActive := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ActiveFileName), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// No active file: default_profile wins (existing behavior).
	cfg, err := mustLoad().Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("without active file = %q, want me (default_profile)", cfg.Name)
	}

	// The active file beats default_profile.
	setActive("friend\n")
	cfg, err = mustLoad().Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "friend" {
		t.Errorf("active file selection = %q, want friend", cfg.Name)
	}

	// No trailing newline and surrounding whitespace are tolerated.
	setActive("  me")
	cfg, err = mustLoad().Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("whitespace/no-newline selection = %q, want me", cfg.Name)
	}

	// An empty active file falls through to default_profile.
	setActive("   \n")
	cfg, err = mustLoad().Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("empty active file selection = %q, want me", cfg.Name)
	}

	// An unknown profile in the active file is an error that lists the rest.
	setActive("nope\n")
	if _, err := mustLoad().Select(""); err == nil {
		t.Error("want error for unknown profile in the active file")
	} else if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "friend") {
		t.Errorf("error should name the bad value and list profiles: %v", err)
	}
}

func TestActiveFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(twoProfiles), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ActiveFileName), []byte("friend\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// An explicit name (the -profile flag) beats the active file.
	cfg, err := f.Select("me")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("explicit selection = %q, want me", cfg.Name)
	}
	// The env var also beats the active file.
	t.Setenv(EnvProfile, "me")
	cfg, err = f.Select("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "me" {
		t.Errorf("env selection = %q, want me", cfg.Name)
	}
}

func TestSetActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(twoProfiles), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetActive("friend"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ActiveFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "friend\n" {
		t.Errorf("active file = %q, want \"friend\\n\"", string(data))
	}
	st, err := os.Stat(filepath.Join(dir, ActiveFileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("active file mode = %o, want 600", st.Mode().Perm())
	}
	// An unknown name is refused and the file is left untouched.
	if err := f.SetActive("nope"); err == nil {
		t.Error("want error for unknown profile name")
	}
	data, _ = os.ReadFile(filepath.Join(dir, ActiveFileName))
	if string(data) != "friend\n" {
		t.Errorf("active file changed after refused set: %q", string(data))
	}
	// EnabledProfile follows the file.
	name, source, err := f.EnabledProfile()
	if err != nil {
		t.Fatal(err)
	}
	if name != "friend" || source == "" {
		t.Errorf("EnabledProfile = (%q, %q), want (friend, non-empty source)", name, source)
	}
}

func TestEnabledProfileFallbacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(twoProfiles), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// No active file: default_profile.
	name, source, err := f.EnabledProfile()
	if err != nil {
		t.Fatal(err)
	}
	if name != "me" || source != "default_profile" {
		t.Errorf("EnabledProfile = (%q, %q), want (me, default_profile)", name, source)
	}
	// A legacy file has the implicit default profile.
	legacy, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	name, source, err = legacy.EnabledProfile()
	if err != nil {
		t.Fatal(err)
	}
	if name != DefaultProfileName || source != "implicit default" {
		t.Errorf("legacy EnabledProfile = (%q, %q), want (default, implicit default)", name, source)
	}
}

func TestSelectUnknownProfile(t *testing.T) {
	file, err := LoadConfig(writeConfig(t, twoProfiles))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Select("nope"); err == nil {
		t.Error("want error for unknown profile")
	} else if !strings.Contains(err.Error(), "friend") || !strings.Contains(err.Error(), "me") {
		t.Errorf("error should list available profiles: %v", err)
	}
	// A legacy file has exactly one profile, "default".
	legacy, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Select("friend"); err == nil {
		t.Error("want error: a legacy file has only the implicit default profile")
	}
}

func TestDefaultProfileMustExist(t *testing.T) {
	content := strings.Replace(twoProfiles, `"default_profile": "me"`, `"default_profile": "someone"`, 1)
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error for unknown default_profile")
	}
}

func TestDefaultProfileInLegacyFileIsAnError(t *testing.T) {
	content := `{"default_profile": "me", "mediamtx_api": "http://127.0.0.1:9997", "ingest_path": "a", "platforms": [{"name": "a", "push_url": "rtmp://h/p"}]}`
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error: default_profile set without a profiles map")
	}
}

func TestMixingProfilesAndTopLevelFieldsIsAnError(t *testing.T) {
	content := `{
  "profiles": {
    "me": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/me",
      "platforms": [{"name": "a", "push_url": "rtmp://h/p"}]
    }
  },
  "platforms": [{"name": "a", "push_url": "rtmp://h/p"}]
}`
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error for top-level platforms alongside a profiles map")
	}
}

func TestProfileNameValidation(t *testing.T) {
	// Replace one profile key with an invalid name.
	cases := []string{
		`"Bad Name"`,
		`"9lead"`,
		`"UPPER"`,
		`"a_b_c_d_e_f_g_h_i_j_k_l_m_n_o_p_q_r_s_t_u_v_w_x"`, // 33 chars
	}
	for _, key := range cases {
		content := strings.Replace(twoProfiles, `"me":`, key+":", 1)
		if _, err := LoadConfig(writeConfig(t, content)); err == nil {
			t.Errorf("want error for profile name %s", key)
		}
	}
}

func TestCrossProfileIngestCollision(t *testing.T) {
	// Same port and path in two profiles: refused.
	content := strings.Replace(twoProfiles, `live/friend`, `live/me`, 1)
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error for duplicate (ingest_port, ingest_path) across profiles")
	}
	// Same path on a different port is fine (different relays).
	content = strings.Replace(twoProfiles, `"ingest_path": "live/friend"`, `"ingest_path": "live/me", "ingest_port": 1936`, 1)
	if _, err := LoadConfig(writeConfig(t, content)); err != nil {
		t.Errorf("same path on a different port should be allowed: %v", err)
	}
	// Default port: two profiles omitting ingest_port must still differ in path.
	content = `{
  "profiles": {
    "a": {"mediamtx_api": "http://127.0.0.1:9997", "ingest_path": "live/a", "platforms": [{"name": "p", "push_url": "rtmp://h/p"}]},
    "b": {"mediamtx_api": "http://127.0.0.1:9997", "ingest_path": "live/a", "platforms": [{"name": "p", "push_url": "rtmp://h/p"}]}
  }
}`
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error: default ingest port must still be checked for collisions")
	}
}

func TestResolveBinaryExplicit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(p, []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, ok := ResolveBinary(p, "ffmpeg"); !ok || got != p {
		t.Errorf("explicit = (%q, %v), want (%q, true)", got, ok, p)
	}
	missing := filepath.Join(dir, "nope")
	if got, ok := ResolveBinary(missing, "ffmpeg"); ok {
		t.Errorf("missing explicit path = (%q, %v), want not ok", got, ok)
	}
}

func TestResolveBinaryPATHAndRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool")
	if err := os.WriteFile(bin, []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	// PATH hit
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, ok := ResolveBinary("", "mytool"); !ok || got != bin {
		t.Errorf("PATH = (%q, %v), want (%q, true)", got, ok, bin)
	}
	// No PATH hit -> runtime dir
	if _, ok := ResolveBinary("", "definitely-missing-tool"); ok {
		t.Error("missing tool should not resolve from PATH")
	}
	rt := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(rt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt, "mytool"), []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	t.Setenv(EnvRuntimeDir, rt)
	if got, ok := ResolveBinary("", "mytool"); !ok || got != filepath.Join(rt, "mytool") {
		t.Errorf("runtime dir = (%q, %v), want (%q, true)", got, ok, filepath.Join(rt, "mytool"))
	}
}

func TestManageMediaMTXRequiresLoopbackAPI(t *testing.T) {
	content := `{
  "mediamtx_api": "http://192.168.1.10:9997",
  "ingest_path": "live/test",
  "manage_mediamtx": true,
  "platforms": [{"name": "a", "push_url": "rtmp://h/p"}]
}`
	if _, err := LoadConfig(writeConfig(t, content)); err == nil {
		t.Error("want error for non-loopback mediamtx_api with manage_mediamtx")
	}
	content = `{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/test",
  "manage_mediamtx": true,
  "mediamtx_path": "/opt/mediamtx",
  "platforms": [{"name": "a", "push_url": "rtmp://h/p"}]
}`
	cfg := loadProfile(t, content, "")
	if !cfg.ManageMediaMTX || cfg.MediaMTXPath != "/opt/mediamtx" {
		t.Errorf("managed fields = (%v, %q)", cfg.ManageMediaMTX, cfg.MediaMTXPath)
	}
}

func TestResolveBinaryBareName(t *testing.T) {
	// A bare command name is looked up on PATH, keeping configs that carry
	// the old default ("ffmpeg") working.
	dir := t.TempDir()
	bin := filepath.Join(dir, "myffmpeg")
	if err := os.WriteFile(bin, []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvRuntimeDir, "")
	if got, ok := ResolveBinary("myffmpeg", "ffmpeg"); !ok || got != bin {
		t.Errorf("bare name = (%q, %v), want (%q, true)", got, ok, bin)
	}
	// A bare name missing everywhere is an error (no fallback to the default
	// name).
	if _, ok := ResolveBinary("definitely-not-a-binary", "ffmpeg"); ok {
		t.Error("missing bare name should not resolve")
	}
	// A bare name falls back to the runtime dir.
	rt := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(rt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt, "myffmpeg"), []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	t.Setenv(EnvRuntimeDir, rt)
	if got, ok := ResolveBinary("myffmpeg", "ffmpeg"); !ok || got != filepath.Join(rt, "myffmpeg") {
		t.Errorf("bare name in runtime dir = (%q, %v)", got, ok)
	}
}
