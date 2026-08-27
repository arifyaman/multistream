package systemd

import "testing"

func TestUnitName(t *testing.T) {
	cases := map[string]string{
		"multistream-kick":         "multistream-kick.service",
		"multistream-kick.service": "multistream-kick.service",
		"":                         ".service",
	}
	for in, want := range cases {
		if got := UnitName(in); got != want {
			t.Errorf("UnitName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseShowOutput(t *testing.T) {
	in := "LoadState=loaded\nActiveState=active\nSubState=running\nNRestarts=3\nMainPID=1234\n"
	vals := parseShowOutput(in)
	if vals["LoadState"] != "loaded" {
		t.Errorf("LoadState = %q", vals["LoadState"])
	}
	if vals["ActiveState"] != "active" {
		t.Errorf("ActiveState = %q", vals["ActiveState"])
	}
	if vals["NRestarts"] != "3" {
		t.Errorf("NRestarts = %q", vals["NRestarts"])
	}
	if vals["MainPID"] != "1234" {
		t.Errorf("MainPID = %q", vals["MainPID"])
	}
	if got := vals["missing"]; got != "" {
		t.Errorf("missing key = %q, want empty", got)
	}
}

func TestParseShowOutputNotLoaded(t *testing.T) {
	vals := parseShowOutput("LoadState=not-found\nActiveState=inactive\nSubState=dead\nNRestarts=0\nMainPID=0\n")
	if vals["LoadState"] != "not-found" {
		t.Errorf("LoadState = %q", vals["LoadState"])
	}
}

func TestParseShowOutputEmpty(t *testing.T) {
	if got := parseShowOutput(""); len(got) != 0 {
		t.Errorf("parseShowOutput(\"\") = %v, want empty", got)
	}
}
