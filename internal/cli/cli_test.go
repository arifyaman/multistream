package cli

import (
	"os"
	"path/filepath"
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

func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(minimalConfig), 0600); err != nil {
		t.Fatal(err)
	}
	return path
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
