package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mateusclira/claude-gc/internal/claude"
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

// Defaults for the tunables.
const (
	DefaultMaxIdle  = 3 * humanize.Day
	DefaultGrace    = 5 * time.Second
	DefaultInterval = 30 * time.Minute
)

// durationFlag is a flag.Value accepting "3d", "36h", "30m" and so on.
type durationFlag struct{ d time.Duration }

func (f *durationFlag) String() string { return humanize.FormatDuration(f.d) }

func (f *durationFlag) Set(s string) error {
	d, err := humanize.ParseDuration(s)
	if err != nil {
		return err
	}
	f.d = d
	return nil
}

// scanFlags are the flags shared by status, run and daemon.
type scanFlags struct {
	maxIdle     durationFlag
	grace       durationFlag
	interval    durationFlag
	entrypoints string
	configDir   string
	verbose     bool
	json        bool
}

// flagSet creates the flag set for a command with the shared flags defined.
func (a *app) flagSet(name string, sf *scanFlags, help string) *flag.FlagSet {
	fs := flag.NewFlagSet("claude-gc "+name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	sf.maxIdle.d = DefaultMaxIdle
	sf.grace.d = DefaultGrace
	sf.interval.d = DefaultInterval
	sf.entrypoints = strings.Join(reaper.DefaultEntrypoints, ",")
	fs.Var(&sf.maxIdle, "max-idle", "reap sessions idle for longer than this")
	fs.Var(&sf.grace, "grace", "wait this long after SIGTERM before SIGKILL")
	fs.StringVar(&sf.entrypoints, "entrypoints", sf.entrypoints, `session entrypoints to reap, comma-separated ("*" for all)`)
	fs.StringVar(&sf.configDir, "config-dir", "", "Claude Code config directory (default $CLAUDE_CONFIG_DIR or ~/.claude)")
	fs.BoolVar(&sf.verbose, "verbose", false, "debug logging")
	fs.Usage = func() {
		fmt.Fprint(a.stderr, help)
		fmt.Fprintln(a.stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	return fs
}

// resolve fills flags that were not given on the command line from the
// environment and validates the result.
func (sf *scanFlags) resolve(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["max-idle"] {
		if err := envDuration("CLAUDE_GC_MAX_IDLE", &sf.maxIdle); err != nil {
			return err
		}
	}
	if !given["grace"] {
		if err := envDuration("CLAUDE_GC_GRACE", &sf.grace); err != nil {
			return err
		}
	}
	if !given["interval"] {
		if err := envDuration("CLAUDE_GC_INTERVAL", &sf.interval); err != nil {
			return err
		}
	}
	if !given["entrypoints"] {
		if v := os.Getenv("CLAUDE_GC_ENTRYPOINTS"); v != "" {
			sf.entrypoints = v
		}
	}
	if sf.maxIdle.d <= 0 {
		return errors.New("--max-idle must be greater than zero")
	}
	if sf.grace.d < 0 {
		return errors.New("--grace must not be negative")
	}
	if sf.interval.d < time.Minute {
		return errors.New("--interval must be at least 1m")
	}
	if len(sf.entrypointList()) == 0 {
		return errors.New("--entrypoints must name at least one entrypoint")
	}
	if sf.configDir == "" {
		sf.configDir = claude.ConfigDir()
	}
	return nil
}

func envDuration(name string, f *durationFlag) error {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	if err := f.Set(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (sf *scanFlags) entrypointList() []string {
	var out []string
	for _, e := range strings.Split(sf.entrypoints, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func (sf *scanFlags) scanOptions() reaper.Options {
	return reaper.Options{
		ConfigDir:   sf.configDir,
		MaxIdle:     sf.maxIdle.d,
		Entrypoints: sf.entrypointList(),
	}
}
