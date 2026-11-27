package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

const daemonHelp = `Usage: claude-gc daemon [flags]

Runs in the foreground and reaps abandoned sessions on every interval. This is
what the background service runs. Logs go to stderr. SIGHUP triggers a pass
immediately; SIGINT or SIGTERM stop the daemon.
`

func (a *app) daemon(args []string) int {
	var sf scanFlags
	fs := a.flagSet("daemon", &sf, daemonHelp)
	fs.Var(&sf.interval, "interval", "time between passes")
	if ok, code := a.parseFlags(fs, &sf, args); !ok {
		return code
	}
	level := slog.LevelInfo
	if sf.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
	log.Info("claude-gc daemon starting",
		"version", a.build.Version,
		"interval", humanize.FormatDuration(sf.interval.d),
		"max_idle", humanize.FormatDuration(sf.maxIdle.d),
		"entrypoints", strings.Join(sf.entrypointList(), ","),
		"config_dir", sf.configDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	pass := func() {
		rep, err := reaper.Scan(sf.scanOptions())
		if err != nil {
			log.Error("scan failed", "error", err)
			return
		}
		for _, p := range rep.Problems {
			log.Warn("problem reading session state", "error", p)
		}
		res := reaper.Apply(rep, reaper.ApplyOptions{Grace: sf.grace.d, Log: log})
		for _, e := range res.Errors {
			log.Warn("reap problem", "error", e)
		}
		if len(res.Reaped) > 0 || len(res.Removed) > 0 {
			log.Info("pass complete",
				"sessions", len(rep.Sessions),
				"reaped", len(res.Reaped),
				"freed", humanize.KB(res.FreedKB),
				"forced", res.Forced,
				"removed_files", len(res.Removed))
			return
		}
		log.Debug("nothing to reap", "sessions", len(rep.Sessions), "max_idle", humanize.FormatDuration(rep.MaxIdle))
	}

	pass()
	ticker := time.NewTicker(sf.interval.d)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("claude-gc daemon stopping")
			return 0
		case <-hup:
			log.Info("received SIGHUP, running a pass now")
			pass()
		case <-ticker.C:
			pass()
		}
	}
}
