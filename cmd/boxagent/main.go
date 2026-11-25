// Command boxagent is a sandboxed coding agent.
package main

import (
	"os"

	"github.com/aaryan-purohit/boxagent/internal/cli"
)

var version = "dev" // set by the Makefile via -ldflags

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(1)
	}
}
