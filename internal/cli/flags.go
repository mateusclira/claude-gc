package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mateusclira/claude-gc/internal/claude"
	"github.com/mateusclira/claude-gc/internal/daemon"
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

// Defaults for the reaping tunables. The schedule defaults live in the
// daemon package.
const (
	DefaultLife  = 3 * humanize.Day
	DefaultGrace = 5 * time.Second
)

// aliases maps flag names from before 0.3.0 to the names that replaced them.
// They keep working, so services installed by older versions still start,
// but help output leaves them out.
var aliases = map[string]string{
	"interval": "fq",
	"max-idle": "life",
}

// durationFlag is a flag.Value accepting "3d", "24h", "30m", "0" and so on.
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

// scanFlags are the flags shared by status, run, daemon and service install.
type scanFlags struct {
	life        durationFlag // -life: how long a session may stay unused
	grace       durationFlag
	fq          durationFlag // -fq: how often the daemon runs a pass
	minUptime   durationFlag
	scheduled   bool // the command takes -fq and -min-uptime
	entrypoints string
	configDir   string
	verbose     bool
	json        bool
}

// flagSet creates the flag set for a command with the shared flags defined.
func (a *app) flagSet(name string, sf *scanFlags, help string) *flag.FlagSet {
	fs := flag.NewFlagSet("claude-gc "+name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	sf.life.d = DefaultLife
	sf.grace.d = DefaultGrace
	sf.fq.d = daemon.DefaultInterval
	sf.minUptime.d = daemon.DefaultMinUptime
	sf.entrypoints = strings.Join(reaper.DefaultEntrypoints, ",")
	fs.Var(&sf.life, "life", "how long a session may stay unused before it is reaped, a `duration` such as 3d or 2h")
	fs.Var(&sf.life, "max-idle", "old name of -life")
	fs.Var(&sf.grace, "grace", "wait this `duration` after SIGTERM before SIGKILL")
	fs.StringVar(&sf.entrypoints, "entrypoints", sf.entrypoints, "comma-separated `list` of session entrypoints to reap, \"*\" for all")
	fs.StringVar(&sf.configDir, "config-dir", "", "Claude Code config `directory` (default $CLAUDE_CONFIG_DIR or ~/.claude)")
	fs.BoolVar(&sf.verbose, "verbose", false, "debug logging")
	fs.Usage = func() {
		fmt.Fprint(a.stderr, help)
		fmt.Fprintln(a.stderr, "\nFlags:")
		a.printFlags(fs)
	}
	return fs
}

// addScheduleFlags registers -fq and -min-uptime for the commands that run on
// a schedule.
func (sf *scanFlags) addScheduleFlags(fs *flag.FlagSet) {
	sf.scheduled = true
	fs.Var(&sf.fq, "fq", "how often to run a pass, a `duration` such as 30m or 2h")
	fs.Var(&sf.fq, "interval", "old name of -fq")
	fs.Var(&sf.minUptime, "min-uptime", "skip passes until the computer has been up this `duration`; 0 disables, and a -life shorter than 24h lowers it")
}

// printFlags prints the flags like flag.PrintDefaults, leaving out aliases.
func (a *app) printFlags(fs *flag.FlagSet) {
	visible := flag.NewFlagSet(fs.Name(), flag.ContinueOnError)
	visible.SetOutput(a.stderr)
	fs.VisitAll(func(f *flag.Flag) {
		if _, alias := aliases[f.Name]; alias {
			return
		}
		visible.Var(f.Value, f.Name, f.Usage)
		visible.Lookup(f.Name).DefValue = f.DefValue
	})
	visible.PrintDefaults()
	fmt.Fprintln(a.stderr, "\nFlags take one dash or two: -fq 2h, --fq 2h and -fq=2h are the same.")
}

// resolve fills flags that were not given on the command line from the
// environment and validates the result.
func (sf *scanFlags) resolve(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		name := f.Name
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		explicit[name] = true
	})
	for _, env := range []struct {
		flag    string
		names   []string // the first one set wins; later names are from before 0.3.0
		value   *durationFlag
		applies bool
	}{
		{"life", []string{"CLAUDE_GC_LIFE", "CLAUDE_GC_MAX_IDLE"}, &sf.life, true},
		{"grace", []string{"CLAUDE_GC_GRACE"}, &sf.grace, true},
		{"fq", []string{"CLAUDE_GC_FQ", "CLAUDE_GC_INTERVAL"}, &sf.fq, sf.scheduled},
		{"min-uptime", []string{"CLAUDE_GC_MIN_UPTIME"}, &sf.minUptime, sf.scheduled},
	} {
		if !env.applies || explicit[env.flag] {
			continue
		}
		set, err := envDuration(env.value, env.names...)
		if err != nil {
			return err
		}
		explicit[env.flag] = set
	}
	if !explicit["entrypoints"] {
		if v := os.Getenv("CLAUDE_GC_ENTRYPOINTS"); v != "" {
			sf.entrypoints = v
		}
	}
	if sf.life.d <= 0 {
		return errors.New("-life must be greater than zero")
	}
	if sf.grace.d < 0 {
		return errors.New("-grace must not be negative")
	}
	if sf.scheduled && sf.fq.d < time.Minute {
		return errors.New("-fq must be at least 1m")
	}
	// The uptime rule exists because nothing can have been unused for longer
	// than the computer has been up. With a life shorter than the default
	// wait, waiting the full default would leave sessions running past
	// their life after every restart, so the wait shrinks to the life.
	if sf.scheduled && !explicit["min-uptime"] {
		sf.minUptime.d = min(daemon.DefaultMinUptime, sf.life.d)
	}
	if len(sf.entrypointList()) == 0 {
		return errors.New("-entrypoints must name at least one entrypoint")
	}
	if sf.configDir == "" {
		sf.configDir = claude.ConfigDir()
	}
	return nil
}

// envDuration sets f from the first of the named environment variables that
// is set, and reports whether one was.
func envDuration(f *durationFlag, names ...string) (bool, error) {
	for _, name := range names {
		v := os.Getenv(name)
		if v == "" {
			continue
		}
		if err := f.Set(v); err != nil {
			return false, fmt.Errorf("%s: %w", name, err)
		}
		return true, nil
	}
	return false, nil
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
		MaxIdle:     sf.life.d,
		Entrypoints: sf.entrypointList(),
	}
}

func (sf *scanFlags) policy() daemon.Policy {
	return daemon.Policy{Interval: sf.fq.d, MinUptime: sf.minUptime.d}
}

// reaps describes what a pass reaps: "claude-vscode sessions idle for longer
// than 3d".
func (sf *scanFlags) reaps() string {
	entrypoints := sf.entrypointList()
	kind := strings.Join(entrypoints, ", ")
	for _, e := range entrypoints {
		if e == "*" {
			kind = "all"
		}
	}
	return fmt.Sprintf("%s sessions idle for longer than %s", kind, humanize.Duration(sf.life.d))
}
