package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mateusclira/claude-gc/internal/daemon"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

const daemonHelp = `Usage: claude-gc daemon [flags]

Runs in the foreground and reaps abandoned sessions on a schedule. This is
what the background service runs.

By default it runs a pass once a day, and only once the computer has been up
for 24 hours. A restart ends every Claude Code session, so right after one
there is nothing to collect. Time asleep counts as uptime, and a pass that
comes due while the computer sleeps runs a few minutes after it wakes. The
daemon remembers its last pass, so restarting it does not cause an extra one.

Logs go to stderr. SIGHUP runs a pass now; SIGINT or SIGTERM stop the daemon.
`

func (a *app) runDaemon(args []string) int {
	var sf scanFlags
	fs := a.flagSet("daemon", &sf, daemonHelp)
	sf.addScheduleFlags(fs)
	if ok, code := a.parseFlags(fs, &sf, args); !ok {
		return code
	}
	level := slog.LevelInfo
	if sf.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))

	statePath, err := daemon.StatePath()
	if err != nil {
		log.Warn("the schedule will not survive restarts", "error", err)
	}
	log.Info("claude-gc daemon starting",
		"version", a.build.Version,
		"schedule", sf.policy().Describe(),
		"reaps", sf.reaps(),
		"config_dir", sf.configDir,
		"state", statePath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	err = daemon.Run(ctx, daemon.Options{
		Policy:    sf.policy(),
		ConfigDir: sf.configDir,
		Reaps:     sf.reaps(),
		StatePath: statePath,
		Log:       log,
		Trigger:   hup,
		Pass: func() (daemon.Pass, error) {
			rep, err := reaper.Scan(sf.scanOptions())
			if err != nil {
				return daemon.Pass{}, err
			}
			for _, p := range rep.Problems {
				log.Warn("problem reading session state", "error", p)
			}
			res := reaper.Apply(rep, reaper.ApplyOptions{Grace: sf.grace.d, Log: log})
			for _, e := range res.Errors {
				log.Warn("reap problem", "error", e)
			}
			return daemon.Pass{
				Sessions: len(rep.Sessions),
				Reaped:   len(res.Reaped),
				FreedKB:  res.FreedKB,
				Forced:   res.Forced,
				Removed:  len(res.Removed),
				Errors:   len(res.Errors),
			}, nil
		},
	})
	if err != nil {
		log.Error("daemon failed", "error", err)
		return 1
	}
	log.Info("claude-gc daemon stopping")
	return 0
}
