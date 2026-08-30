package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xlip/multistream/internal/check"
	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/daemonipc"
	"github.com/xlip/multistream/internal/report"
	"github.com/xlip/multistream/internal/state"
	"github.com/xlip/multistream/internal/supervisor"
)

// usageText is printed by -h/--help and on unknown commands.
const usageText = `multistream - status of OBS -> mediamtx -> platforms

Usage:
  multistream [status] [flags]        one-shot status (default command)
  multistream status --watch          live refresh, prints events on change
  multistream status --json           machine-readable (lines in --watch)
  multistream check                   probe API, daemon, endpoints, key files
  multistream restart <platform|relay>  ask the daemon to restart a re-broadcaster
                                        or the managed relay
  multistream daemon                  run the supervisor (spawn + watch ffmpeg,
                                        and the relay when manage_mediamtx is set)
  multistream config                  show effective config

Flags:
  -config string    config file (default: $MULTISTREAM_CONFIG, per-user config
                    dir, /etc/multistream/config.json, ./config.json)
  -version          print version and exit
  -h, --help        show this help

status flags:
  --watch, -w       keep refreshing
  --interval, -i n  refresh interval in seconds (default from config, 2)
  --json            JSON output
  --no-color        disable ANSI colors

Exit codes: 0 all healthy, 1 something down, 2 usage/config error.
`

// stateDirOrEmpty resolves the state directory path, returning "" on error
// (read-only commands still work, just without daemon awareness).
func stateDirOrEmpty() string {
	d, err := state.DirPath()
	if err != nil {
		return ""
	}
	return d
}

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
	c := report.NewCollector(cfg, stateDirOrEmpty())
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

// runRestart asks the running daemon to restart one platform. It refuses
// when the daemon is not running, since a restart without a supervisor
// would leave the re-broadcaster unsupervised.
func runRestart(cfg *config.Config, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: multistream restart <platform>")
		return 2
	}
	name := args[0]
	if name == supervisor.RelayName {
		if !cfg.ManageMediaMTX {
			fmt.Fprintln(os.Stderr, "multistream: the relay is not managed by this daemon (manage_mediamtx is off)")
			return 2
		}
	} else if _, ok := cfg.PlatformByName(name); !ok {
		fmt.Fprintf(os.Stderr, "multistream: unknown platform %q (see: multistream config)\n", name)
		return 2
	}
	dir := stateDirOrEmpty()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "multistream: cannot resolve state directory")
		return 1
	}
	network, addr := state.IPCNetworkAddr(dir)
	if err := daemonipc.Restart(network, addr, name); err != nil {
		fmt.Fprintf(os.Stderr, "multistream: cannot restart %s: %v\n", name, err)
		fmt.Fprintln(os.Stderr, "  the daemon is not running - a restart here would be unsupervised.")
		fmt.Fprintln(os.Stderr, "  start it with: multistream daemon")
		return 1
	}
	fmt.Printf("%s restart requested\n", name)
	return 0
}

// runDaemon runs the supervisor in the foreground until interrupted.
func runDaemon(cfg *config.Config) int {
	dir, err := state.StateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "multistream:", err)
		return 2
	}
	sup, err := supervisor.New(cfg, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "multistream daemon:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	what := "platforms: " + strings.Join(platformNames(cfg), ", ")
	if cfg.ManageMediaMTX {
		what += "; relay: managed"
	}
	fmt.Printf("multistream daemon running (state %s), %s\n", dir, what)
	if err := sup.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "multistream daemon:", err)
		return 1
	}
	return 0
}

func runConfig(cfg *config.Config) {
	fmt.Printf("config file:  %s\n", cfg.Source())
	fmt.Printf("mediamtx api: %s\n", cfg.MediaMTXAPI)
	fmt.Printf("ingest:       %s (port %d)\n", cfg.IngestPath, cfg.IngestPort)
	fmt.Printf("refresh:      %ds\n", cfg.RefreshSec)
	if cfg.FFmpegPath != "" {
		fmt.Printf("ffmpeg:       %s (explicit)\n", cfg.FFmpegPath)
	}
	if cfg.ManageMediaMTX {
		detail := "managed by the daemon"
		if cfg.MediaMTXPath != "" {
			detail += ", " + cfg.MediaMTXPath + " (explicit)"
		}
		fmt.Printf("relay:        %s\n", detail)
	}
	fmt.Printf("supervisor:   restart after %ds, limit %d restarts in %ds\n",
		cfg.RestartSec, cfg.StartLimitBurst, cfg.StartLimitIntervalSec)
	if cfg.AwayFile != "" {
		fmt.Printf("away file:    %s\n", cfg.AwayFile)
	}
	if cfg.KeysDir != "" {
		fmt.Printf("keys dir:     %s\n", cfg.KeysDir)
	}
	fmt.Println("platforms:")
	for i := range cfg.Platforms {
		p := &cfg.Platforms[i]
		fmt.Printf("  %-10s push=%s", p.Name, p.PushURL)
		if kf := cfg.KeyFile(p); kf != "" {
			fmt.Printf("  key=%s", kf)
		}
		fmt.Println()
	}
}

func platformNames(cfg *config.Config) []string {
	names := make([]string, len(cfg.Platforms))
	for i := range cfg.Platforms {
		names[i] = cfg.Platforms[i].Name
	}
	return names
}
