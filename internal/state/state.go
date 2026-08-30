// Package state owns the on-disk state the multistream daemon writes and
// the read-only commands (status, check) consume: per-platform pid files,
// the supervisor state document, and the IPC endpoint location. Keeping it
// in its own package means the supervisor (writer) and the report/check
// readers share one definition of where and what these files are.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// envState overrides the state directory (used by tests and unusual
// layouts). Unset by default.
const envState = "MULTISTREAM_STATE"

// DirPath returns the multistream state directory path without creating it.
// It follows the per-OS conventions (XDG state dir on Linux,
// %LOCALAPPDATA% on Windows, ~/Library/Application Support on macOS) and can
// be overridden with $MULTISTREAM_STATE for tests and unusual layouts.
func DirPath() (string, error) {
	if d := os.Getenv(envState); d != "" {
		return d, nil
	}
	base, err := stateBaseDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(base, "multistream"), nil
}

// StateDir returns the multistream state directory, creating it if needed.
func StateDir() (string, error) {
	d, err := DirPath()
	if err != nil {
		return "", err
	}
	return d, os.MkdirAll(d, 0o700)
}

// stateBaseDir returns the per-OS base directory the state dir lives under.
func stateBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return local, nil
		}
		return filepath.Join(home, "AppData", "Local"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	default:
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			return xdg, nil
		}
		return filepath.Join(home, ".local", "state"), nil
	}
}

// PidFile returns the path of a platform's pid file.
func PidFile(dir, platform string) string {
	return filepath.Join(dir, platform+".pid")
}

// DaemonPidFile returns the path of the daemon's own pid file.
func DaemonPidFile(dir string) string {
	return filepath.Join(dir, "daemon.pid")
}

// SupervisorFile returns the path of the supervisor state document.
func SupervisorFile(dir string) string {
	return filepath.Join(dir, "supervisor.json")
}

// RelayConfigFile returns the path of the mediamtx config the daemon
// generates for the managed relay.
func RelayConfigFile(dir string) string {
	return filepath.Join(dir, "mediamtx.generated.yml")
}

// IPCNetworkAddr returns the network and address of the daemon IPC endpoint.
// Unix uses a socket inside the state dir; Windows uses a named pipe.
func IPCNetworkAddr(dir string) (string, string) {
	if runtime.GOOS == "windows" {
		return "tcp", `\\.\pipe\multistream`
	}
	return "unix", filepath.Join(dir, "multistream.sock")
}

// PlatformState is the supervisor's view of one re-broadcaster.
type PlatformState struct {
	PID       int       `json:"pid"`
	Restarts  int       `json:"restarts"`
	State     string    `json:"state"` // "running" | "restarting" | "failed" | "stopped"
	LastError string    `json:"last_error,omitempty"`
	Since     time.Time `json:"since,omitempty"`
}

// RelayState is the supervisor's view of the mediamtx relay, present only
// when the daemon manages it (manage_mediamtx).
type RelayState struct {
	// Managed is true when manage_mediamtx is on (the daemon owns or tracks
	// the relay); false would mean an unmanaged external relay, which the
	// daemon does not publish at all, so in practice it is always true.
	Managed   bool      `json:"managed"`
	Mode      string    `json:"mode"` // "spawned" (daemon owns the process) or "external" (API already reachable at start)
	PID       int       `json:"pid"`
	Restarts  int       `json:"restarts"`
	State     string    `json:"state"` // "running" | "restarting" | "failed" | "stopped"
	LastError string    `json:"last_error,omitempty"`
	Since     time.Time `json:"since,omitempty"`
}

// SupervisorState is the document the daemon publishes for status/check.
type SupervisorState struct {
	Updated   time.Time                `json:"updated"`
	DaemonPID int                      `json:"daemon_pid"`
	Platforms map[string]PlatformState `json:"platforms"`
	Relay     *RelayState              `json:"relay,omitempty"`
}

// LoadSupervisorState reads the supervisor state document. It returns nil
// when the file is absent (no daemon has run yet).
func LoadSupervisorState(dir string) (*SupervisorState, error) {
	data, err := os.ReadFile(SupervisorFile(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st SupervisorState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SupervisorFile(dir), err)
	}
	return &st, nil
}

// IsFresh reports whether the supervisor state was updated within maxAge,
// i.e. the daemon that wrote it is (probably) still running.
func (st *SupervisorState) IsFresh(maxAge time.Duration) bool {
	if st == nil {
		return false
	}
	return time.Since(st.Updated) < maxAge
}

// ReadPid reads a pid file and returns the pid and whether it parsed.
func ReadPid(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(data))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// WriteFile writes data to path atomically (temp file + rename) so readers
// never observe a partial document.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}
