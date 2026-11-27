package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mateusclira/claude-gc/internal/daemon"
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/proc"
	"github.com/mateusclira/claude-gc/internal/service"
)

const serviceHelp = `Usage: claude-gc service <command> [flags]

Commands:
  install     Install and start the background service
              (a launchd agent on macOS, a systemd user unit on Linux)
  uninstall   Stop the daemon and remove the service
  status      Show the schedule, the last pass and the next one
  logs        Show recent daemon logs (-f to follow)

By default the daemon runs a pass once a day, and only once the computer has
been up for 24 hours: a restart ends every Claude Code session, so right
after one there is nothing to collect.

install accepts the shared flags (--max-idle, --entrypoints, --grace,
--config-dir, --verbose) plus:
  --interval <dur>     Time between passes (default 24h)
  --min-uptime <dur>   Hold passes back until the computer has been up this
                       long (default 24h; 0 disables)
  --binary <path>      claude-gc executable to run (default: this one)

Run install again with new flags to change the settings.
`

func (a *app) service(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, serviceHelp)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, serviceHelp)
		return 0
	case "install", "uninstall", "status", "logs":
	default:
		fmt.Fprintf(a.stderr, "claude-gc: unknown service command %q\n\n%s", cmd, serviceHelp)
		return 2
	}
	mgr, err := service.New()
	if err != nil {
		return a.fail(err)
	}
	switch cmd {
	case "install":
		return a.serviceInstall(mgr, rest)
	case "uninstall":
		return a.serviceUninstall(mgr, rest)
	case "status":
		return a.serviceStatus(mgr, rest)
	default:
		return a.serviceLogs(mgr, rest)
	}
}

func (a *app) serviceInstall(mgr service.Manager, args []string) int {
	var sf scanFlags
	var binary string
	fs := a.flagSet("service install", &sf, serviceHelp)
	sf.addScheduleFlags(fs)
	fs.StringVar(&binary, "binary", "", "claude-gc executable to run (default: this one)")
	if ok, code := a.parseFlags(fs, &sf, args); !ok {
		return code
	}
	if binary == "" {
		binary, _ = resolveBinary()
	}
	if binary == "" {
		return a.fail(errors.New("could not determine the path of this executable; pass --binary"))
	}
	cfg := service.Config{
		Binary:      binary,
		Interval:    sf.interval.d,
		MinUptime:   sf.minUptime.d,
		MaxIdle:     sf.maxIdle.d,
		Entrypoints: sf.entrypointList(),
		Grace:       sf.grace.d,
		Verbose:     sf.verbose,
	}
	// The service does not inherit the shell environment, so a non-default
	// config directory has to travel on the command line.
	if home, err := os.UserHomeDir(); err != nil || filepath.Clean(sf.configDir) != filepath.Join(home, ".claude") {
		cfg.ConfigDir = sf.configDir
	}
	notes, err := mgr.Install(cfg)
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "Installed %s\n", shortenHome(mgr.UnitPath()))
	st := waitRunning(mgr, 5*time.Second)

	// Show what the new daemon will do, worked out the way it works it out:
	// from its policy and the last pass it finds in the state file.
	planned := daemon.State{
		Reaps:            sf.reaps(),
		IntervalSeconds:  int64(cfg.Interval / time.Second),
		MinUptimeSeconds: int64(cfg.MinUptime / time.Second),
	}
	if prev, err := loadDaemonState(); err == nil && prev.ConfigDir == sf.configDir {
		planned.LastPass, planned.LastError = prev.LastPass, prev.LastError
	}
	rows := [][2]string{serviceRow(st)}
	boot, bootErr := proc.BootTime()
	rows = append(rows, scheduleRows(planned, st.Running, wallNow(), boot, bootErr)...)
	rows = append(rows, [2]string{"Logs", shortenHome(mgr.LogHint())})
	printRows(a.stdout, rows)
	if !st.Running {
		fmt.Fprintln(a.stdout, "The daemon is not running yet. Check `claude-gc service logs`.")
	}
	for _, n := range notes {
		fmt.Fprintf(a.stdout, "Note: %s\n", n)
	}
	return 0
}

func (a *app) serviceUninstall(mgr service.Manager, args []string) int {
	if len(args) > 0 {
		fmt.Fprint(a.stderr, serviceHelp)
		return 2
	}
	if err := mgr.Uninstall(); err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "Removed %s. The daemon has been stopped.\n", shortenHome(mgr.UnitPath()))
	return 0
}

func (a *app) serviceStatus(mgr service.Manager, args []string) int {
	if len(args) > 0 {
		fmt.Fprint(a.stderr, serviceHelp)
		return 2
	}
	st, err := mgr.Status()
	if err != nil {
		return a.fail(err)
	}
	if !st.Installed {
		fmt.Fprintln(a.stdout, "Service: not installed. Run `claude-gc service install` to set it up.")
		return 1
	}
	rows := [][2]string{serviceRow(st)}
	state, err := loadDaemonState()
	if err != nil {
		rows = append(rows, [2]string{"State", "unreadable: " + err.Error()})
	} else {
		boot, bootErr := proc.BootTime()
		rows = append(rows, scheduleRows(state, st.Running, wallNow(), boot, bootErr)...)
	}
	rows = append(rows,
		[2]string{"Unit", shortenHome(st.UnitPath)},
		[2]string{"Logs", shortenHome(st.LogHint)})
	printRows(a.stdout, rows)
	if !st.Running {
		return 1
	}
	return 0
}

func (a *app) serviceLogs(mgr service.Manager, args []string) int {
	fs := flag.NewFlagSet("claude-gc service logs", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	follow := fs.Bool("f", false, "follow the log")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := mgr.Logs(a.stdout, *follow); err != nil {
		return a.fail(err)
	}
	return 0
}

func serviceRow(st service.Status) [2]string {
	if st.Running {
		return [2]string{"Service", fmt.Sprintf("running (pid %d)", st.PID)}
	}
	return [2]string{"Service", "installed but not running"}
}

// scheduleRows describes the daemon's schedule and its last and next passes.
func scheduleRows(state daemon.State, running bool, now, boot time.Time, bootErr error) [][2]string {
	var rows [][2]string
	policy := state.Policy()
	if policy.Interval > 0 {
		rows = append(rows, [2]string{"Schedule", policy.Describe()})
	}
	if state.Reaps != "" {
		rows = append(rows, [2]string{"Reaps", state.Reaps})
	}
	if bootErr == nil {
		rows = append(rows, [2]string{"Uptime", humanize.Duration(now.Sub(boot))})
	}
	rows = append(rows, [2]string{"Last pass", lastPassText(state.LastPass, now, boot, bootErr)})
	if f := state.FailedSinceLastPass(); f != nil {
		rows = append(rows, [2]string{"Last error", humanize.Ago(f.At, now) + ": " + f.Error})
	}
	if running && policy.Interval > 0 && bootErr == nil {
		rows = append(rows, [2]string{"Next pass", nextPassText(state, now, boot)})
	}
	return rows
}

func lastPassText(p *daemon.Pass, now, boot time.Time, bootErr error) string {
	if p == nil {
		return "none yet"
	}
	s := humanize.Ago(p.At, now)
	if p.Reaped == 0 {
		s += ", nothing to reap"
	} else {
		s += fmt.Sprintf(", reaped %s and freed %s", humanize.Plural(p.Reaped, "session"), humanize.KB(p.FreedKB))
	}
	if p.Errors > 0 {
		s += fmt.Sprintf(", %s (see the logs)", humanize.Plural(p.Errors, "error"))
	}
	if bootErr == nil && p.At.Before(boot) {
		s += ", before the last restart"
	}
	return s
}

func nextPassText(state daemon.State, now, boot time.Time) string {
	next := state.Next(now, boot)
	if !now.Before(next) {
		return "now"
	}
	s := fmt.Sprintf("%s (%s)", humanize.Until(next, now), next.Local().Format("Mon Jan 2 15:04"))
	if p := state.Policy(); p.WaitingForUptime(boot, next) {
		s += ", once uptime reaches " + humanize.Span(p.MinUptime)
	}
	return s
}

func printRows(w io.Writer, rows [][2]string) {
	for _, r := range rows {
		fmt.Fprintf(w, "%-11s %s\n", r[0]+":", r[1])
	}
}

func loadDaemonState() (daemon.State, error) {
	path, err := daemon.StatePath()
	if err != nil {
		return daemon.State{}, err
	}
	return daemon.LoadState(path)
}

// wallNow is the current wall-clock time without a monotonic reading, the
// form every schedule computation uses.
func wallNow() time.Time { return time.Now().Round(0) }

func waitRunning(mgr service.Manager, timeout time.Duration) service.Status {
	deadline := time.Now().Add(timeout)
	for {
		st, err := mgr.Status()
		if err == nil && st.Running {
			return st
		}
		if time.Now().After(deadline) {
			return st
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// resolveBinary returns a stable absolute path to this executable. When the
// executable is reachable through PATH (for example a Homebrew symlink that
// survives upgrades) that path is preferred over the resolved real path.
func resolveBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		real = exe
	}
	if onPath, err := exec.LookPath(filepath.Base(exe)); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			if r, err := filepath.EvalSymlinks(abs); err == nil && r == real {
				return abs, nil
			}
		}
	}
	return real, nil
}
