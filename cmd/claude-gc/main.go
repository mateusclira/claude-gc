// Command claude-gc finds and reaps abandoned Claude Code sessions.
//
// See https://github.com/mateusclira/claude-gc for documentation.
package main

import (
	"os"
	"runtime/debug"

	"github.com/mateusclira/claude-gc/internal/cli"
)

// Set at build time with -ldflags (see Makefile and .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], buildInfo()))
}

// buildInfo prefers the values stamped by the release build and falls back
// to what the Go toolchain recorded, so `go install ...@v1.2.3` still
// reports its version and a build from a checkout reports its commit.
func buildInfo() cli.BuildInfo {
	bi := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return bi
	}
	if bi.Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		bi.Version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if bi.Commit == "none" && len(s.Value) >= 7 {
				bi.Commit = s.Value[:7]
			}
		case "vcs.time":
			if bi.Date == "unknown" && s.Value != "" {
				bi.Date = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" && bi.Commit != "none" {
				bi.Commit += "-dirty"
			}
		}
	}
	return bi
}
