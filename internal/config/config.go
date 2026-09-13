// Package config loads and validates the multistream configuration file.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// EnvRuntimeDir is set by the npm CLI shim to the directory holding the
// bundled runtime binaries (ffmpeg, mediamtx) downloaded at install time.
const EnvRuntimeDir = "MULTISTREAM_RUNTIME_DIR"

// EnvProfile selects a profile when -profile is not given.
const EnvProfile = "MULTISTREAM_PROFILE"

// ActiveFileName is the sidecar file (next to the config file) that holds
// the enabled profile name, one line. Written by `multistream switch`,
// read by every command and the daemon when no explicit profile is given.
const ActiveFileName = "active"

// DefaultProfileName is the name of the implicit profile in a legacy config
// file (one without a "profiles" map).
const DefaultProfileName = "default"

// profileNameRe bounds profile names: they become directory names and part
// of a Windows pipe name, so they stay conservative.
var profileNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

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

// Config is one profile's configuration: one user's streaming chain (the
// relay endpoint to pull from, the keys, and the platforms to re-broadcast
// to). A config file holds one or more profiles (see File).
type Config struct {
	// Name is the profile name this config was loaded under ("default" in a
	// legacy file). It is not part of the JSON: the loader sets it from the
	// profile key or from DefaultProfileName.
	Name string `json:"-"`
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
	// FFmpegPath is the ffmpeg binary the daemon spawns. Default: resolved
	// at runtime as PATH, then the bundled runtime dir (see ResolveBinary).
	// Set this to pin a specific binary.
	FFmpegPath string `json:"ffmpeg_path,omitempty"`
	// ManageMediaMTX makes the daemon spawn and supervise the mediamtx relay
	// itself instead of expecting an external one. When true, the daemon
	// generates a mediamtx config from this file, spawns the relay, and
	// restarts it on exit - so a fresh machine needs no separate mediamtx
	// install or service. Default false (use an externally managed mediamtx).
	ManageMediaMTX bool `json:"manage_mediamtx,omitempty"`
	// MediaMTXPath is the mediamtx binary used when ManageMediaMTX is true.
	// Default: resolved at runtime as PATH, then the bundled runtime dir.
	MediaMTXPath string `json:"mediamtx_path,omitempty"`
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

// File is a loaded config file: a set of named profiles, or a single
// implicit profile in a legacy file that has no "profiles" map.
type File struct {
	DefaultProfile string
	profiles       map[string]*Config
	src            string
}

// Source returns the file path the file was loaded from.
func (f *File) Source() string { return f.src }

// ProfileNames returns the profile names in the file, sorted.
func (f *File) ProfileNames() []string {
	names := make([]string, 0, len(f.profiles))
	for n := range f.profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Select resolves a profile: an explicit name (the -profile flag), else the
// MULTISTREAM_PROFILE env var, else the enabled profile from the active
// file, else default_profile, else a profile named "default".
func (f *File) Select(name string) (*Config, error) {
	if name == "" {
		name = os.Getenv(EnvProfile)
	}
	if name == "" {
		active, err := ReadActive(f.ActiveFilePath())
		if err != nil {
			return nil, err
		}
		name = active
	}
	if name == "" {
		if f.DefaultProfile != "" {
			name = f.DefaultProfile
		} else {
			name = DefaultProfileName
		}
	}
	if cfg, ok := f.profiles[name]; ok {
		return cfg, nil
	}
	if name == DefaultProfileName && f.DefaultProfile == "" {
		return nil, fmt.Errorf("no profile named %q and no default_profile set; pass -profile or set default_profile (available: %s)",
			name, strings.Join(f.ProfileNames(), ", "))
	}
	return nil, fmt.Errorf("profile %q not found (available: %s)", name, strings.Join(f.ProfileNames(), ", "))
}

// ActiveFilePath returns the path of this file's active file: the config
// file's own directory plus ActiveFileName.
func (f *File) ActiveFilePath() string {
	return filepath.Join(filepath.Dir(f.src), ActiveFileName)
}

// ReadActive reads an enabled profile name from an active file. A missing or
// empty file yields "" (no switch made); surrounding whitespace is trimmed.
func ReadActive(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read active file %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SetActive writes the enabled profile name to the active file next to the
// config file. The name must be a profile of this file. The write is atomic
// (temp file + rename) and the file ends up 0600.
func (f *File) SetActive(name string) error {
	if _, ok := f.profiles[name]; !ok {
		return fmt.Errorf("profile %q not found (available: %s)", name, strings.Join(f.ProfileNames(), ", "))
	}
	path := f.ActiveFilePath()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".active-*")
	if err != nil {
		return fmt.Errorf("write active file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.WriteString(name + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write active file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write active file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write active file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write active file: %w", err)
	}
	return nil
}

// EnabledProfile resolves the enabled profile without any explicit
// selection: the active file if it names a profile of this file, else
// default_profile, else a profile named "default". The source string names
// where the answer came from.
func (f *File) EnabledProfile() (string, string, error) {
	name, err := ReadActive(f.ActiveFilePath())
	if err != nil {
		return "", "", err
	}
	if name != "" {
		if _, ok := f.profiles[name]; !ok {
			return "", "", fmt.Errorf("active file %s names unknown profile %q (available: %s)",
				f.ActiveFilePath(), name, strings.Join(f.ProfileNames(), ", "))
		}
		return name, "active file " + f.ActiveFilePath(), nil
	}
	if f.DefaultProfile != "" {
		return f.DefaultProfile, "default_profile", nil
	}
	if _, ok := f.profiles[DefaultProfileName]; ok {
		return DefaultProfileName, "implicit default", nil
	}
	return "", "", fmt.Errorf("no enabled profile: no active file, no default_profile and no profile named %q (available: %s)",
		DefaultProfileName, strings.Join(f.ProfileNames(), ", "))
}

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

// ResolveBinary locates a runtime dependency (ffmpeg, mediamtx) the daemon
// needs to spawn. It tries, in order: an explicit path from the config, the
// system PATH, and the bundled runtime dir (the binaries the npm postinstall
// downloaded, exposed via $MULTISTREAM_RUNTIME_DIR). It returns the resolved
// path and whether a usable binary was found. An explicit path that does not
// exist is an error, not a fallback, so a typo does not silently switch to a
// different binary. A bare command name (no path separator), such as the
// long-used "ffmpeg", is looked up on PATH and in the runtime dir like any
// name, keeping old configs working.
func ResolveBinary(explicit, name string) (string, bool) {
	if explicit != "" && !isBareName(explicit) {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit, true
		}
		return "", false
	}
	probe := name
	if explicit != "" {
		probe = explicit
	}
	if p, err := exec.LookPath(probe); err == nil {
		return p, true
	}
	if dir := os.Getenv(EnvRuntimeDir); dir != "" {
		candidate := filepath.Join(dir, probe)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// isBareName reports whether s is a plain command name rather than a path
// (it contains neither path separator).
func isBareName(s string) bool {
	return !strings.Contains(s, "/") && !strings.Contains(s, string(os.PathSeparator))
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

// LoadConfig reads and validates the config file at path (or the first
// default location that exists when path is empty) and returns its
// profiles. A file without a "profiles" map is a legacy single-profile
// file: its top-level fields are one implicit profile named "default".
func LoadConfig(path string) (*File, error) {
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
	var f File
	f.src = path
	if err := f.parse(data); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &f, nil
}

// fileDoc is the new multi-profile file shape.
type fileDoc struct {
	DefaultProfile string             `json:"default_profile"`
	Profiles       map[string]*Config `json:"profiles"`
}

func (f *File) parse(data []byte) error {
	var doc fileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	f.DefaultProfile = doc.DefaultProfile
	f.profiles = map[string]*Config{}

	if len(doc.Profiles) == 0 {
		// Legacy single-profile file: the top-level fields are one implicit
		// profile named "default".
		if f.DefaultProfile != "" {
			return fmt.Errorf("default_profile is set but the file has no profiles map")
		}
		var c Config
		if err := json.Unmarshal(data, &c); err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		c.Name = DefaultProfileName
		c.src = f.src
		f.profiles[DefaultProfileName] = &c
		return f.validate()
	}

	// A profiles map and top-level profile fields are two different file
	// shapes; mixing them is always a mistake, so refuse it.
	var probe Config
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if probe.MediaMTXAPI != "" || probe.IngestPath != "" || probe.IngestPort != 0 ||
		probe.RefreshSec != 0 || probe.KeysDir != "" || probe.AwayFile != "" ||
		probe.FFmpegPath != "" || probe.ManageMediaMTX || probe.MediaMTXPath != "" ||
		probe.RestartSec != 0 || probe.StartLimitIntervalSec != 0 || probe.StartLimitBurst != 0 ||
		len(probe.Platforms) > 0 {
		return fmt.Errorf("top-level profile fields (mediamtx_api, ingest_path, platforms, ...) cannot be mixed with a profiles map; move them into profiles.<name>")
	}

	for name, c := range doc.Profiles {
		if !profileNameRe.MatchString(name) {
			return fmt.Errorf("profile name %q is invalid: must match %s", name, profileNameRe.String())
		}
		c.Name = name
		c.src = f.src
		f.profiles[name] = c
	}
	return f.validate()
}

func (f *File) validate() error {
	for _, name := range f.ProfileNames() {
		if err := f.profiles[name].validate(); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	if f.DefaultProfile != "" {
		if _, ok := f.profiles[f.DefaultProfile]; !ok {
			return fmt.Errorf("default_profile %q not found (available: %s)",
				f.DefaultProfile, strings.Join(f.ProfileNames(), ", "))
		}
	}
	// Two profiles must never pull the same relay stream (same port and
	// path): they would re-broadcast the same feed, and the daemon's
	// process-level lookups (orphan cleanup, the stateless status fallback)
	// could no longer tell their ffmpeg processes apart.
	seen := map[int]string{}
	for _, name := range f.ProfileNames() {
		c := f.profiles[name]
		if other, ok := seen[c.IngestPort]; ok && other == c.IngestPath {
			return fmt.Errorf("profiles %q and %q both use ingest port %d with path %q",
				other, name, c.IngestPort, c.IngestPath)
		}
		seen[c.IngestPort] = c.IngestPath
	}
	return nil
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
	if c.ManageMediaMTX {
		// The managed relay binds the API itself on this machine, so the
		// API must not point somewhere else (and must not be exposed to
		// the network).
		if u, err := url.Parse(c.MediaMTXAPI); err == nil && u.Host != "" {
			host := u.Hostname()
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				return fmt.Errorf("manage_mediamtx requires mediamtx_api on a loopback address, got %q", u.Host)
			}
		}
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
