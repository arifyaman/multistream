package version

import (
	"strings"
	"testing"
)

func TestStringDevBuild(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	defer func() { Version, Commit, Date = oldV, oldC, oldD }()

	Version, Commit, Date = "dev", "none", ""
	if got := String(); got != "multistream version dev" {
		t.Errorf("String() = %q", got)
	}
}

func TestStringReleaseBuild(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	defer func() { Version, Commit, Date = oldV, oldC, oldD }()

	Version = "2027.1.0"
	Commit = "abc1234"
	Date = "2026-08-28T00:00:00Z"
	got := String()
	for _, want := range []string{"multistream version 2027.1.0", "commit abc1234", "2026-08-28T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
