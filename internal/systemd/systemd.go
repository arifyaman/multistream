// Package systemd queries unit state via systemctl and the journal.
package systemd

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

// queryTimeout bounds systemctl lookups.
const queryTimeout = 5 * time.Second

// journalTimeout bounds journalctl reads.
const journalTimeout = 10 * time.Second

// UnitState is the normalized state of a systemd unit.
type UnitState struct {
	Exists   bool
	Active   string
	Sub      string
	Restarts int
	PID      int
}

// UnitName normalizes a unit name to a .service name.
func UnitName(u string) string {
	if strings.HasSuffix(u, ".service") {
		return u
	}
	return u + ".service"
}

// parseShowOutput parses `systemctl show -p ...` key=value output.
func parseShowOutput(out string) map[string]string {
	vals := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			vals[line[:i]] = line[i+1:]
		}
	}
	return vals
}

// QueryUnit asks systemd for a unit's state. It is read-only.
func QueryUnit(ctx context.Context, unit string) (UnitState, error) {
	var st UnitState
	name := UnitName(unit)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show",
		"-p", "LoadState", "-p", "ActiveState", "-p", "SubState",
		"-p", "NRestarts", "-p", "MainPID", "--", name).Output()
	if err != nil {
		return st, fmt.Errorf("systemctl show %s: %w", name, err)
	}
	vals := parseShowOutput(string(out))
	st.Exists = vals["LoadState"] == "loaded"
	st.Active = vals["ActiveState"]
	st.Sub = vals["SubState"]
	st.Restarts, _ = strconv.Atoi(vals["NRestarts"])
	st.PID, _ = strconv.Atoi(vals["MainPID"])
	return st, nil
}

// LastUnitError returns the last error-priority journal line for a unit,
// or "" when there are none. Used to surface push failures.
func LastUnitError(ctx context.Context, unit string) string {
	name := UnitName(unit)
	ctx, cancel := context.WithTimeout(ctx, journalTimeout)
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
func RestartUnit(ctx context.Context, unit string) error {
	name := UnitName(unit)
	var cmd *exec.Cmd
	if os.Geteuid() != 0 {
		cmd = exec.CommandContext(ctx, "sudo", "systemctl", "restart", name)
	} else {
		cmd = exec.CommandContext(ctx, "systemctl", "restart", name)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
