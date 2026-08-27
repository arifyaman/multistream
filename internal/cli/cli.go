// Package cli implements the multistream command-line interface:
// global flags, command dispatch and the per-command runners.
package cli

import (
	"fmt"
	"os"

	"github.com/xlip/multistream/internal/config"
	"github.com/xlip/multistream/internal/version"
)

// Execute parses args and runs the selected command. It returns the
// process exit code: 0 all healthy, 1 something down, 2 usage/config
// error.
func Execute(args []string) int {
	cfgPath := ""
	showVersion := false
	rest := append([]string(nil), args...)
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "-config", "--config":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "multistream: -config needs a value")
				return 2
			}
			cfgPath = rest[i+1]
			rest = append(rest[:i], rest[i+2:]...)
			i--
		case "-version", "--version":
			showVersion = true
			rest = append(rest[:i], rest[i+1:]...)
			i--
		case "-h", "--help", "help":
			fmt.Print(usageText)
			return 0
		default:
			// First positional: the command. Stop scanning for globals.
			rest = rest[i:]
			i = len(rest)
		}
	}

	if showVersion {
		fmt.Println(version.String())
		return 0
	}

	cmd, rest := "status", rest
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "multistream:", err)
		return 2
	}

	switch cmd {
	case "status":
		return runStatus(cfg, rest)
	case "check":
		return runCheck(cfg)
	case "restart":
		return runRestart(cfg, rest)
	case "config":
		runConfig(cfg)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "multistream: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}
