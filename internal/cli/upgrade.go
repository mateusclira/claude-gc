package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/mateusclira/claude-gc/internal/service"
	"github.com/mateusclira/claude-gc/internal/upgrade"
)

const upgradeHelp = `Usage: claude-gc upgrade [flags]

Replaces this claude-gc with the latest release from GitHub, after checking
the download against the release's published checksums. If the background
service runs this copy, it is restarted on the new version with the same
settings.

Flags:
  -check            Only say whether a newer release exists
  -version <tag>    Install this release instead of the latest, e.g. v0.3.1
`

func (a *app) upgrade(args []string) int {
	fs := flag.NewFlagSet("claude-gc upgrade", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() { fmt.Fprint(a.stderr, upgradeHelp) }
	check := fs.Bool("check", false, "only say whether a newer release exists")
	want := fs.String("version", "", "release `tag` to install")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprint(a.stderr, upgradeHelp)
		return 2
	}

	exe, err := os.Executable()
	if err != nil {
		return a.fail(fmt.Errorf("could not find this executable: %w", err))
	}
	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		target = exe
	}
	opts := upgrade.Options{
		Current:   a.build.Version,
		Want:      *want,
		Target:    target,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		UserAgent: "claude-gc/" + a.build.DisplayVersion(),
		Progress:  func(line string) { fmt.Fprintln(a.stdout, line) },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *check {
		res, err := upgrade.Check(ctx, opts)
		if err != nil {
			return a.fail(err)
		}
		switch {
		case res.Updated:
			fmt.Fprintf(a.stdout, "claude-gc %s is available; you have %s. Run `claude-gc upgrade` to install it.\n", res.To, res.From)
		case res.From == res.To:
			fmt.Fprintf(a.stdout, "claude-gc %s is the latest release. You are up to date.\n", res.To)
		default:
			fmt.Fprintf(a.stdout, "You have claude-gc %s, newer than the latest release, %s.\n", res.From, res.To)
		}
		return 0
	}

	res, err := upgrade.Run(ctx, opts)
	if err != nil {
		return a.fail(err)
	}
	if !res.Updated {
		if res.From == res.To {
			fmt.Fprintf(a.stdout, "claude-gc %s is already the latest release.\n", res.To)
		} else {
			fmt.Fprintf(a.stdout, "You have claude-gc %s, newer than the latest release, %s. Nothing to do.\n", res.From, res.To)
		}
		return 0
	}
	from := res.From
	if from == "" {
		from = a.build.DisplayVersion()
	}
	fmt.Fprintf(a.stdout, "Updated claude-gc from %s to %s at %s\n", from, res.To, shortenHome(target))
	a.restartService(res.To, exe, target)
	return 0
}

// restartService moves the background service to the new binary when it
// runs this copy, and says what to do when it runs another one.
func (a *app) restartService(version string, paths ...string) {
	mgr, err := service.New()
	if err != nil {
		return
	}
	if st, err := mgr.Status(); err != nil || !st.Installed {
		return
	}
	if resolved, err := resolveBinary(); err == nil {
		paths = append(paths, resolved)
	}
	for _, p := range paths {
		if service.Runs(mgr, p) {
			if err := mgr.Restart(); err != nil {
				fmt.Fprintf(a.stdout, "Could not restart the background service: %v\nRun `claude-gc service install` to move it to %s.\n", err, version)
				return
			}
			fmt.Fprintf(a.stdout, "Restarted the background service on %s, with the same settings.\n", version)
			return
		}
	}
	fmt.Fprintf(a.stdout, "Note: the background service runs another copy of claude-gc (see %s).\nRun `claude-gc service install` with this copy to move it to %s.\n", shortenHome(mgr.UnitPath()), version)
}
