package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/service"
)

const serviceHelp = `Usage: claude-gc service <command> [flags]

Commands:
  install     Install and start the background service
              (a launchd agent on macOS, a systemd user unit on Linux)
  uninstall   Stop the daemon and remove the service
  status      Show whether the service is installed and running
  logs        Show recent daemon logs (-f to follow)

install accepts the shared flags (--max-idle, --entrypoints, --grace,
--config-dir, --verbose) plus:
  --interval <dur>   Time between passes (default 30m)
  --binary <path>    claude-gc executable to run (default: this one)
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
	fs.Var(&sf.interval, "interval", "time between passes")
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
	if st.Running {
		fmt.Fprintf(a.stdout, "claude-gc daemon is running (pid %d). Every %s it reaps %s sessions idle for longer than %s.\n",
			st.PID, humanize.Duration(cfg.Interval), strings.Join(cfg.Entrypoints, ","), humanize.Duration(cfg.MaxIdle))
	} else {
		fmt.Fprintln(a.stdout, "The service is installed but not running yet. Check `claude-gc service status` and the logs.")
	}
	fmt.Fprintf(a.stdout, "Logs: %s\n", shortenHome(mgr.LogHint()))
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
	switch {
	case !st.Installed:
		fmt.Fprintln(a.stdout, "Service: not installed. Run `claude-gc service install` to set it up.")
		return 1
	case st.Running:
		fmt.Fprintf(a.stdout, "Service: installed and running (pid %d)\n", st.PID)
	default:
		fmt.Fprintln(a.stdout, "Service: installed but not running")
	}
	fmt.Fprintf(a.stdout, "Unit:    %s\nLogs:    %s\n", shortenHome(st.UnitPath), shortenHome(st.LogHint))
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
