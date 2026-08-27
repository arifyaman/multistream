package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// UnitState is the normalized state of a systemd unit.
type UnitState struct {
	Exists   bool
	Active   string
	Sub      string
	Restarts int
	PID      int
}

// unitName normalizes a unit name to a .service name.
func unitName(u string) string {
	if strings.HasSuffix(u, ".service") {
		return u
	}
	return u + ".service"
}

// QueryUnit asks systemd for a unit's state. It is read-only.
func QueryUnit(unit string) (UnitState, error) {
	var st UnitState
	name := unitName(unit)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show",
		"-p", "LoadState", "-p", "ActiveState", "-p", "SubState",
		"-p", "NRestarts", "-p", "MainPID", "--", name).Output()
	if err != nil {
		return st, fmt.Errorf("systemctl show %s: %w", name, err)
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			vals[line[:i]] = line[i+1:]
		}
	}
	st.Exists = vals["LoadState"] == "loaded"
	st.Active = vals["ActiveState"]
	st.Sub = vals["SubState"]
	st.Restarts, _ = strconv.Atoi(vals["NRestarts"])
	st.PID, _ = strconv.Atoi(vals["MainPID"])
	return st, nil
}

// LastUnitError returns the last error-priority journal line for a unit,
// or "" when there are none. Used to surface push failures.
func LastUnitError(unit string) string {
	name := unitName(unit)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", "-u", name, "--no-pager", "-q",
		"-n", "300", "-p", "err", "--output=cat")
	out, _ := cmd.Output()
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	l := lines[len(lines)-1]
	if len(l) > 160 {
		l = "..." + l[len(l)-157:]
	}
	return l
}

// RestartUnit restarts a unit, escalating to sudo when not root.
func RestartUnit(unit string) error {
	name := unitName(unit)
	var cmd *exec.Cmd
	if os.Geteuid() != 0 {
		cmd = exec.Command("sudo", "systemctl", "restart", name)
	} else {
		cmd = exec.Command("systemctl", "restart", name)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
