package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const usageText = `multistream - status of OBS -> mediamtx -> platforms

Usage:
  multistream [status] [flags]        one-shot status (default command)
  multistream status --watch          live refresh, prints events on change
  multistream status --json           machine-readable (lines in --watch)
  multistream check                   probe API, units, endpoints, key files
  multistream restart <platform>      restart one re-broadcaster
  multistream config                  show effective config

Flags:
  -config string    config file (default: $MULTISTREAM_CONFIG,
                    /etc/multistream/config.json, ./config.json)
  -h, --help        show this help

status flags:
  --watch, -w       keep refreshing
  --interval, -i n  refresh interval in seconds (default from config, 2)
  --json            JSON output
  --no-color        disable ANSI colors

Exit codes: 0 all healthy, 1 something down, 2 usage/config error.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfgPath := ""
	rest := args
	for i := 0; ; i++ {
		if i >= len(rest) {
			break
		}
		a := rest[i]
		switch a {
		case "-config", "--config":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "multistream: -config needs a value")
				return 2
			}
			cfgPath = rest[i+1]
			rest = rest[i+2:]
			i = -1
		case "-h", "--help", "help":
			fmt.Print(usageText)
			return 0
		default:
			rest = rest[i:]
			i = len(rest)
		}
	}

	cmd, rest := "status", rest
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "multistream:", err)
		return 2
	}

	switch cmd {
	case "status":
		return cmdStatus(cfg, rest)
	case "check":
		return cmdCheck(cfg)
	case "restart":
		return cmdRestart(cfg, rest)
	case "config":
		cmdConfig(cfg)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "multistream: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}

func cmdStatus(cfg *Config, args []string) int {
	watch := false
	jsonOut := false
	noColor := false
	interval := time.Duration(cfg.RefreshSec) * time.Second
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--watch", "-w":
			watch = true
		case "--interval", "-i":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "multistream: --interval needs a value (seconds)")
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintln(os.Stderr, "multistream: invalid --interval", args[i])
				return 2
			}
			interval = time.Duration(n) * time.Second
		case "--json":
			jsonOut = true
		case "--no-color":
			noColor = true
		default:
			fmt.Fprintf(os.Stderr, "multistream: unknown status flag %q\n", args[i])
			return 2
		}
	}

	colored := colorEnabled(noColor)
	c := NewCollector(cfg)

	if !watch {
		rep, err := c.Collect()
		if err != nil {
			fmt.Fprintln(os.Stderr, "multistream:", err)
			return 2
		}
		if jsonOut {
			printJSON(rep)
		} else {
			fmt.Print(renderTable(rep, colored))
		}
		if rep.OK {
			return 0
		}
		return 1
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var prev *StatusReport
	for {
		rep, err := c.Collect()
		if err == nil {
			if jsonOut {
				printJSONLine(rep)
			} else {
				for _, e := range rep.Events(prev) {
					fmt.Println(e)
				}
				fmt.Print("\x1b[2J\x1b[H")
				fmt.Print(renderTable(rep, colored))
			}
			prev = rep
		} else {
			fmt.Fprintf(os.Stderr, "multistream: collect: %v\n", err)
		}
		select {
		case <-sig:
			return 0
		case <-ticker.C:
		}
	}
}

func cmdCheck(cfg *Config) int {
	code := 0
	fmt.Printf("config file:  %s\n", cfg.Source())

	client := newMediaMTXClient(cfg.MediaMTXAPI)
	if v, err := client.Version(); err == nil {
		fmt.Printf("mediamtx:     OK  version %s  api %s\n", v, cfg.MediaMTXAPI)
	} else {
		code = 1
		fmt.Printf("mediamtx:     FAIL  api %s  %v\n", cfg.MediaMTXAPI, err)
	}

	for _, p := range cfg.Platforms {
		st, err := QueryUnit(p.Unit)
		if err != nil || !st.Exists {
			code = 1
			fmt.Printf("%-10s FAIL  unit %s not found\n", p.Name, unitName(p.Unit))
			continue
		}
		host, port := parsePushURL(p.PushURL)
		line := fmt.Sprintf("unit %s (%s/%s)", unitName(p.Unit), st.Active, st.Sub)
		if host == "" {
			code = 1
			line += "  FAIL push_url missing host"
			fmt.Printf("%-10s %s\n", p.Name, line)
			continue
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			code = 1
			fmt.Printf("%-10s %s  FAIL endpoint %s:%d unreachable (%v)\n", p.Name, line, host, port, err)
			continue
		}
		conn.Close()
		line += fmt.Sprintf("  endpoint %s:%d reachable", host, port)
		if kf := cfg.KeyFile(&p); kf != "" {
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

func cmdRestart(cfg *Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: multistream restart <platform>")
		return 2
	}
	p, ok := cfg.PlatformByName(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "multistream: unknown platform %q (see: multistream config)\n", args[0])
		return 2
	}
	if err := RestartUnit(p.Unit); err != nil {
		fmt.Fprintln(os.Stderr, "multistream:", err)
		return 1
	}
	fmt.Printf("%s restarted\n", unitName(p.Unit))
	return 0
}

func cmdConfig(cfg *Config) {
	fmt.Printf("config file:  %s\n", cfg.Source())
	fmt.Printf("mediamtx api: %s\n", cfg.MediaMTXAPI)
	fmt.Printf("ingest:       %s (port %d)\n", cfg.IngestPath, cfg.IngestPort)
	fmt.Printf("refresh:      %ds\n", cfg.RefreshSec)
	if cfg.KeysDir != "" {
		fmt.Printf("keys dir:     %s\n", cfg.KeysDir)
	}
	fmt.Println("platforms:")
	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		fmt.Printf("  %-10s unit=%-28s push=%s", p.Name, unitName(p.Unit), p.PushURL)
		if kf := cfg.KeyFile(p); kf != "" {
			fmt.Printf("  key=%s", kf)
		}
		fmt.Println()
	}
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
