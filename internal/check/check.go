// Package check probes the multistream deployment without streaming:
// mediamtx API reachability, unit existence, platform endpoint
// reachability (TCP dial only) and key file presence.
package check

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/mediamtx"
	"github.com/xlip/multistream/internal/systemd"
)

// dialTimeout bounds endpoint probes.
const dialTimeout = 5 * time.Second

// Run performs all checks, prints a report, and returns a process exit
// code (0 = all healthy).
func Run(ctx context.Context, cfg *config.Config) int {
	code := 0
	fmt.Printf("config file:  %s\n", cfg.Source())

	client := mediamtx.NewClient(cfg.MediaMTXAPI)
	if v, err := client.Version(ctx); err == nil {
		fmt.Printf("mediamtx:     OK  version %s  api %s\n", v, cfg.MediaMTXAPI)
	} else {
		code = 1
		fmt.Printf("mediamtx:     FAIL  api %s  %v\n", cfg.MediaMTXAPI, err)
	}

	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		st, err := systemd.QueryUnit(ctx, p.Unit)
		if err != nil || !st.Exists {
			code = 1
			fmt.Printf("%-10s FAIL  unit %s not found\n", p.Name, systemd.UnitName(p.Unit))
			continue
		}
		host, port := parsePushURL(p.PushURL)
		line := fmt.Sprintf("unit %s (%s/%s)", systemd.UnitName(p.Unit), st.Active, st.Sub)
		if host == "" {
			code = 1
			line += "  FAIL push_url missing host"
			fmt.Printf("%-10s %s\n", p.Name, line)
			continue
		}
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			code = 1
			fmt.Printf("%-10s %s  FAIL endpoint %s:%d unreachable (%v)\n", p.Name, line, host, port, err)
			continue
		}
		conn.Close()
		line += fmt.Sprintf("  endpoint %s:%d reachable", host, port)
		if kf := cfg.KeyFile(p); kf != "" {
			if _, err := os.Stat(kf); err != nil {
				code = 1
				line += "  FAIL key file " + kf + " missing"
			} else {
				line += "  key file " + kf
			}
		}
		fmt.Printf("%-10s OK  %s\n", p.Name, line)
	}
	return code
}

// parsePushURL extracts the host and port of an rtmp(s) push URL
// (default port 1935 for rtmp, 443 for rtmps when absent), for endpoint
// probing.
func parsePushURL(s string) (string, int) {
	pu, err := url.Parse(s)
	if err != nil || pu.Host == "" {
		return "", 0
	}
	host := pu.Host
	port := 1935
	if pu.Scheme == "rtmps" {
		port = 443
	}
	if h, p, err := net.SplitHostPort(pu.Host); err == nil {
		host = h
		if n, err2 := strconv.Atoi(p); err2 == nil && n > 0 {
			port = n
		}
	}
	return host, port
}
