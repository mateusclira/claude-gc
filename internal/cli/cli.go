// Package cli implements the claude-gc command line.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

// BuildInfo describes how the binary was built. Empty fields are unknown.
type BuildInfo struct {
	Version   string // "0.3.0" or "v0.3.0"; empty for a development build
	Commit    string // short commit hash, with "-dirty" for a modified tree
	Date      string // commit date, RFC 3339
	GoInstall bool   // built by `go install module@version`, which records no commit
}

// DisplayVersion is the version as people see it: "v0.3.0", or "dev".
func (b BuildInfo) DisplayVersion() string {
	v := b.Version
	if v == "" {
		return "dev"
	}
	if v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return v
}

// VersionLine is what `claude-gc version` prints. It names only what the
// binary knows about itself, so a `go install` build says so instead of
// reporting an unknown commit.
func (b BuildInfo) VersionLine() string {
	var parts []string
	if b.Commit != "" {
		parts = append(parts, "commit "+b.Commit)
	}
	if b.Date != "" {
		day := b.Date
		if t, err := time.Parse(time.RFC3339, b.Date); err == nil {
			day = t.UTC().Format(time.DateOnly)
		}
		parts = append(parts, day)
	}
	if b.GoInstall {
		parts = append(parts, "go install")
	}
	parts = append(parts, runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH)
	return "claude-gc " + b.DisplayVersion() + " (" + strings.Join(parts, ", ") + ")"
}

type app struct {
	stdout io.Writer
	stderr io.Writer
	build  BuildInfo
	color  bool
	tty    bool
}

// Main runs the command line and returns the process exit code.
func Main(args []string, build BuildInfo) int {
	a := &app{stdout: os.Stdout, stderr: os.Stderr, build: build}
	a.tty = isTerminal(os.Stdout)
	a.color = a.tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return a.run(args)
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usageText)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "status":
		return a.status(rest)
	case "run":
		return a.runOnce(rest)
	case "daemon":
		return a.runDaemon(rest)
	case "service":
		return a.service(rest)
	case "upgrade":
		return a.upgrade(rest)
	case "version", "--version", "-v":
		a.version()
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, usageText)
		return 0
	default:
		fmt.Fprintf(a.stderr, "claude-gc: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}

const usageText = `claude-gc reaps abandoned Claude Code sessions.

Usage:
  claude-gc <command> [flags]

Commands:
  status     Show every Claude Code session and what would happen to it
  run        Reap abandoned sessions once, right now (-dry-run to preview)
  daemon     Run continuously, reaping once a day
  service    Install, inspect or remove the background service
  upgrade    Upgrade claude-gc to the latest release (-check to only look)
  version    Print version information

Main flags:
  -life <dur>          How long a session may stay unused before it is
                       reaped (default 3d)
  -fq <dur>            How often the daemon runs a pass (default 24h);
                       daemon and service install only

Other flags:
  -min-uptime <dur>    Skip passes until the computer has been up this long
                       (default 24h, or -life if shorter; 0 disables);
                       daemon and service install only
  -entrypoints <list>  Session entrypoints to reap, comma-separated
                       (default claude-vscode; "*" for all)
  -grace <dur>         Wait this long after SIGTERM before SIGKILL (default 5s)
  -config-dir <path>   Claude Code config directory
                       (default $CLAUDE_CONFIG_DIR or ~/.claude)
  -json                Machine-readable output (status and run)
  -verbose             Debug logging

Durations use d, h, m and s, such as 3d, 24h, 2h, 30m or 1d12h, and 0.
Flags take one dash or two: -fq 2h, --fq 2h and -fq=2h are the same.

Environment:
  CLAUDE_GC_LIFE, CLAUDE_GC_FQ, CLAUDE_GC_MIN_UPTIME, CLAUDE_GC_ENTRYPOINTS,
  CLAUDE_GC_GRACE and CLAUDE_CONFIG_DIR set defaults for the flags above.

Examples:
  claude-gc status -life 2h                  what would a 2-hour life reap?
  claude-gc run -dry-run                     preview a pass
  claude-gc service install -fq 2h -life 3d  run every 2 hours, 3-day life

Run "claude-gc <command> -h" for the flags of one command.
`

func (a *app) version() {
	fmt.Fprintln(a.stdout, a.build.VersionLine())
}

// fail reports an error and returns the exit code for it.
func (a *app) fail(err error) int {
	fmt.Fprintf(a.stderr, "claude-gc: %v\n", err)
	return 1
}

// parseFlags parses args into fs and resolves environment defaults. ok is
// false when the caller should return code immediately.
func (a *app) parseFlags(fs *flag.FlagSet, sf *scanFlags, args []string) (ok bool, code int) {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return false, 0
	}
	if err != nil {
		return false, 2 // the flag package already reported it
	}
	if err := sf.resolve(fs); err != nil {
		fmt.Fprintf(a.stderr, "claude-gc: %v\n", err)
		return false, 2
	}
	return true, 0
}

func (a *app) printJSON(v any) int {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return a.fail(err)
	}
	return 0
}

func (a *app) warnProblems(problems []error) {
	for _, p := range problems {
		fmt.Fprintf(a.stderr, "claude-gc: warning: %v\n", p)
	}
}

// logger returns a structured logger writing to stderr. Without verbose it
// only reports warnings, so command output stays clean.
func (a *app) logger(verbose bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
}

func (a *app) paint(code, s string) string {
	if !a.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// shortenHome replaces the home directory prefix of p with "~".
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
