// Command multistream prints the status of the OBS -> mediamtx ->
// platforms streaming chain. All logic lives in the internal packages.
package main

import (
	"os"

	"github.com/xlip/multistream/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
