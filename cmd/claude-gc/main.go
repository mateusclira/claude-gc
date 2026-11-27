// Command claude-gc finds and reaps abandoned Claude Code sessions.
//
// See https://github.com/mateusclira/claude-gc for documentation.
package main

import (
	"os"

	"github.com/mateusclira/claude-gc/internal/cli"
)

// Set at build time with -ldflags (see Makefile and .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
