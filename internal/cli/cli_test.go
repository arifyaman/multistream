package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xlip/multistream/internal/version"
)

const minimalConfig = `{
  "mediamtx_api": "http://127.0.0.1:9997",
  "ingest_path": "live/test",
  "platforms": [
    {"name": "twitch", "push_url": "rtmp://h/app/k"}
  ]
}`

const profilesConfig = `{
  "default_profile": "me",
  "profiles": {
    "me": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/me",
      "platforms": [{"name": "twitch", "push_url": "rtmp://h/k"}]
    },
    "friend": {
      "mediamtx_api": "http://127.0.0.1:9997",
      "ingest_path": "live/friend",
      "platforms": [{"name": "youtube", "push_url": "rtmp://h/k"}]
    }
  }
}`

func writeConfigContent(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConfig(t *testing.T) string {
	return writeConfigContent(t, minimalConfig)
}

func TestExecuteVersion(t *testing.T) {
	old := version.Version
	defer func() { version.Version = old }()
	version.Version = "testver"

	// -version must work with no config file at all.
	if code := Execute([]string{"-version"}); code != 0 {
		t.Errorf("Execute(-version) = %d, want 0", code)
	}
}

func TestExecuteHelp(t *testing.T) {
	if code := Execute([]string{"-h"}); code != 0 {
		t.Errorf("Execute(-h) = %d, want 0", code)
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	if code := Execute([]string{"bogus"}); code != 2 {
		t.Errorf("Execute(bogus) = %d, want 2", code)
	}
}

func TestExecuteMissingConfig(t *testing.T) {
	t.Setenv("MULTISTREAM_CONFIG", "")
	if code := Execute([]string{"-config", "/nonexistent/config.json", "check"}); code != 2 {
		t.Errorf("Execute(check, missing config) = %d, want 2", code)
	}
}

func TestExecuteRestartUsage(t *testing.T) {
	path := writeConfig(t)
	if code := Execute([]string{"-config", path, "restart"}); code != 2 {
		t.Errorf("Execute(restart without platform) = %d, want 2", code)
	}
	if code := Execute([]string{"-config", path, "restart", "nope"}); code != 2 {
		t.Errorf("Execute(restart unknown platform) = %d, want 2", code)
	}
}

func TestExecuteConfigCommand(t *testing.T) {
	path := writeConfig(t)
	if code := Execute([]string{"-config", path, "config"}); code != 0 {
		t.Errorf("Execute(config) = %d, want 0", code)
	}
}

func TestExecuteStatusFlagsValidation(t *testing.T) {
	path := writeConfig(t)
	if code := Execute([]string{"-config", path, "status", "--bogus"}); code != 2 {
		t.Errorf("Execute(status --bogus) = %d, want 2", code)
	}
	if code := Execute([]string{"-config", path, "status", "--interval"}); code != 2 {
		t.Errorf("Execute(status --interval without value) = %d, want 2", code)
	}
	if code := Execute([]string{"-config", path, "status", "--interval", "0"}); code != 2 {
		t.Errorf("Execute(status --interval 0) = %d, want 2", code)
	}
}

func TestExecuteProfileSelection(t *testing.T) {
	// Without -profile the default profile is used.
	path := writeConfigContent(t, profilesConfig)
	if code := Execute([]string{"-config", path, "config"}); code != 0 {
		t.Errorf("Execute(config) = %d, want 0", code)
	}
	// An explicit -profile selects another profile.
	if code := Execute([]string{"-config", path, "-profile", "friend", "config"}); code != 0 {
		t.Errorf("Execute(-profile friend config) = %d, want 0", code)
	}
	// An unknown profile is a usage error.
	if code := Execute([]string{"-config", path, "-profile", "nope", "config"}); code != 2 {
		t.Errorf("Execute(-profile nope config) = %d, want 2", code)
	}
	// -profile without a value is a usage error.
	if code := Execute([]string{"-config", path, "-profile"}); code != 2 {
		t.Errorf("Execute(-profile without value) = %d, want 2", code)
	}
	// A legacy file has only the implicit default profile.
	legacy := writeConfig(t)
	if code := Execute([]string{"-config", legacy, "-profile", "friend", "config"}); code != 2 {
		t.Errorf("Execute(legacy, -profile friend) = %d, want 2", code)
	}
}

func TestExecuteSwitch(t *testing.T) {
	path := writeConfigContent(t, profilesConfig)
	dir := filepath.Dir(path)

	// Enabling a profile writes the active file next to the config.
	if code := Execute([]string{"-config", path, "switch", "friend"}); code != 0 {
		t.Errorf("Execute(switch friend) = %d, want 0", code)
	}
	data, err := os.ReadFile(filepath.Join(dir, "active"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "friend\n" {
		t.Errorf("active file = %q, want \"friend\\n\"", string(data))
	}
	// The next command without -profile follows the active file.
	out := captureStdout(t, []string{"-config", path, "config"})
	if !strings.Contains(out, "profile:      friend") {
		t.Errorf("config after switch should select friend: %s", out)
	}
	// An unknown profile is a usage error and leaves the file untouched.
	if code := Execute([]string{"-config", path, "switch", "nope"}); code != 2 {
		t.Errorf("Execute(switch nope) = %d, want 2", code)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "active"))
	if string(data) != "friend\n" {
		t.Errorf("active file changed after refused switch: %q", string(data))
	}
	// Too many arguments is a usage error.
	if code := Execute([]string{"-config", path, "switch", "a", "b"}); code != 2 {
		t.Errorf("Execute(switch a b) = %d, want 2", code)
	}
	// No argument prints the enabled profile.
	out = captureStdout(t, []string{"-config", path, "switch"})
	if !strings.Contains(out, "enabled profile: friend") {
		t.Errorf("bare switch should print the enabled profile: %s", out)
	}
}

// captureStdout runs Execute(args), fails the test on a non-zero exit code
// and returns what the command wrote to stdout.
func captureStdout(t *testing.T, args []string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := Execute(args)
	w.Close()
	os.Stdout = old
	if code != 0 {
		t.Fatalf("Execute(%v) = %d, want 0", args, code)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
