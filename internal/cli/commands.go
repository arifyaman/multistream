package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/xlip/multistream/internal/check"
	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/report"
	"github.com/xlip/multistream/internal/systemd"
)

// usageText is printed by -h/--help and on unknown commands.
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
  -version          print version and exit
  -h, --help        show this help

status flags:
  --watch, -w       keep refreshing
  --interval, -i n  refresh interval in seconds (default from config, 2)
  --json            JSON output
  --no-color        disable ANSI colors

Exit codes: 0 all healthy, 1 something down, 2 usage/config error.
`

func runStatus(cfg *config.Config, args []string) int {
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

	colored := report.ColorEnabled(noColor)
	c := report.NewCollector(cfg)
	ctx := context.Background()

	if !watch {
		rep, err := c.Collect(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "multistream:", err)
			return 2
		}
		if jsonOut {
			b, err := rep.JSON()
			if err != nil {
				fmt.Fprintln(os.Stderr, "multistream:", err)
				return 2
			}
			fmt.Println(string(b))
		} else {
			fmt.Print(rep.Render(colored))
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
	var prev *report.StatusReport
	for {
		rep, err := c.Collect(ctx)
		if err == nil {
			if jsonOut {
				if b, err := rep.JSON(); err == nil {
					fmt.Println(string(b))
				}
			} else {
				for _, e := range rep.Events(prev) {
					fmt.Println(e)
				}
				fmt.Print("\x1b[2J\x1b[H")
				fmt.Print(rep.Render(colored))
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

func runCheck(cfg *config.Config) int {
	return check.Run(context.Background(), cfg)
}

func runRestart(cfg *config.Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: multistream restart <platform>")
		return 2
	}
	p, ok := cfg.PlatformByName(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "multistream: unknown platform %q (see: multistream config)\n", args[0])
		return 2
	}
	if err := systemd.RestartUnit(context.Background(), p.Unit); err != nil {
		fmt.Fprintln(os.Stderr, "multistream:", err)
		return 1
	}
	fmt.Printf("%s restarted\n", systemd.UnitName(p.Unit))
	return 0
}

func runConfig(cfg *config.Config) {
	fmt.Printf("config file:  %s\n", cfg.Source())
	fmt.Printf("mediamtx api: %s\n", cfg.MediaMTXAPI)
	fmt.Printf("ingest:       %s (port %d)\n", cfg.IngestPath, cfg.IngestPort)
	fmt.Printf("refresh:      %ds\n", cfg.RefreshSec)
	if cfg.AwayFile != "" {
		fmt.Printf("away file:    %s\n", cfg.AwayFile)
	}
	if cfg.KeysDir != "" {
		fmt.Printf("keys dir:     %s\n", cfg.KeysDir)
	}
	fmt.Println("platforms:")
	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		fmt.Printf("  %-10s unit=%-28s push=%s", p.Name, systemd.UnitName(p.Unit), p.PushURL)
		if kf := cfg.KeyFile(p); kf != "" {
			fmt.Printf("  key=%s", kf)
		}
		fmt.Println()
	}
}
