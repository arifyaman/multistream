// Package check probes the multistream deployment without streaming:
// mediamtx API reachability, the daemon, each platform's push endpoint
// (TCP dial only), and key file presence.
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
	"github.com/xlip/multistream/internal/daemonipc"
	"github.com/xlip/multistream/internal/mediamtx"
	"github.com/xlip/multistream/internal/state"
)

// dialTimeout bounds endpoint probes.
const dialTimeout = 5 * time.Second

// awayMinVersion is the first mediamtx release with the always-available
// (away file) feature.
const awayMinVersion = "1.16.3"

// Run performs all checks, prints a report, and returns a process exit
// code (0 = all healthy).
func Run(ctx context.Context, cfg *config.Config) int {
	code := 0
	fmt.Printf("config file:  %s\n", cfg.Source())

	client := mediamtx.NewClient(cfg.MediaMTXAPI)
	mtxVersion := ""
	haveVersion := false
	if v, err := client.Version(ctx); err == nil {
		mtxVersion, haveVersion = v, true
		fmt.Printf("mediamtx:     OK  version %s  api %s\n", v, cfg.MediaMTXAPI)
	} else {
		code = 1
		fmt.Printf("mediamtx:     FAIL  api %s  %v\n", cfg.MediaMTXAPI, err)
	}

	if cfg.AwayFile != "" && !checkAwayFile(cfg.AwayFile, mtxVersion, haveVersion) {
		code = 1
	}

	if cfg.ManageMediaMTX {
		if bin, ok := config.ResolveBinary(cfg.MediaMTXPath, "mediamtx"); ok {
			fmt.Printf("relay:        OK  managed, mediamtx %s\n", bin)
		} else {
			code = 1
			fmt.Println("relay:        FAIL  manage_mediamtx is set but no mediamtx binary was found")
		}
	}

	if daemonUp := checkDaemon(); daemonUp {
		fmt.Println("daemon:       OK  running (supervising the re-broadcasters)")
	} else {
		code = 1
		fmt.Println("daemon:       FAIL  not running (start it with: multistream daemon)")
	}

	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		host, port := parsePushURL(p.PushURL)
		if host == "" {
			code = 1
			fmt.Printf("%-10s FAIL  push_url missing host\n", p.Name)
			continue
		}
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			code = 1
			fmt.Printf("%-10s FAIL  endpoint %s:%d unreachable (%v)\n", p.Name, host, port, err)
			continue
		}
		conn.Close()
		line := fmt.Sprintf("endpoint %s:%d reachable", host, port)
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

// checkDaemon reports whether a daemon is currently serving IPC.
func checkDaemon() bool {
	dir, err := state.DirPath()
	if err != nil {
		return false
	}
	network, addr := state.IPCNetworkAddr(dir)
	return daemonipc.Ping(network, addr) == nil
}

// checkAwayFile verifies the configured away file: it must exist, be a
// non-empty regular file, and the mediamtx version (when known) must be
// new enough to play it.
func checkAwayFile(path, mtxVersion string, haveVersion bool) bool {
	st, err := os.Stat(path)
	if err != nil {
		fmt.Printf("away file:    FAIL  %s  %v\n", path, err)
		return false
	}
	if st.IsDir() || st.Size() == 0 {
		fmt.Printf("away file:    FAIL  %s  empty or not a regular file\n", path)
		return false
	}
	if haveVersion {
		if ok, err := mediamtx.VersionAtLeast(mtxVersion, awayMinVersion); err == nil && !ok {
			fmt.Printf("away file:    FAIL  %s  requires mediamtx >= %s (have %s)\n",
				path, awayMinVersion, mtxVersion)
			return false
		}
	}
	fmt.Printf("away file:    OK  %s  %s\n", path, humanSize(st.Size()))
	return true
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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
