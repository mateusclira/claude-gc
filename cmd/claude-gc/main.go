// Command claude-gc finds and reaps abandoned Claude Code sessions.
//
// See https://github.com/mateusclira/claude-gc for documentation.
package main

import (
	"os"
	"runtime/debug"

	"github.com/mateusclira/claude-gc/internal/cli"
)

// Set at build time with -ldflags (see Makefile and .goreleaser.yaml). Left
// empty, they are filled in from what the Go toolchain recorded.
var (
	version string
	commit  string
	date    string
)

func main() {
	stamped := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	info, _ := debug.ReadBuildInfo()
	os.Exit(cli.Main(os.Args[1:], resolveBuildInfo(stamped, info)))
}

// resolveBuildInfo prefers the values stamped by the release build and falls
// back to what the Go toolchain recorded: a build from a checkout records its
// commit, and `go install ...@v1.2.3` records its version. Anything still
// unknown is left empty rather than given a placeholder.
func resolveBuildInfo(bi cli.BuildInfo, info *debug.BuildInfo) cli.BuildInfo {
	if info == nil {
		return bi
	}
	if bi.Version == "" && info.Main.Version != "(devel)" {
		bi.Version = info.Main.Version
	}
	vcs := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			vcs = true
			if bi.Commit == "" && len(s.Value) >= 7 {
				bi.Commit = s.Value[:7]
			}
		case "vcs.time":
			if bi.Date == "" {
				bi.Date = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" && bi.Commit != "" {
				bi.Commit += "-dirty"
			}
		}
	}
	// `go install module@version` builds the copy of the source held by the
	// module proxy. It has no Git metadata, so there is no commit to report,
	// but the toolchain verified it against the checksum database (Main.Sum).
	bi.GoInstall = bi.Commit == "" && !vcs && info.Main.Sum != ""
	return bi
}
