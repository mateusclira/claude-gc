package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/proc"
)

// DefaultCheckEvery bounds how long the daemon sleeps between looks at the
// clock. Timers do not run while the computer sleeps, so a pass that comes
// due during sleep runs at most this long after the computer wakes.
const DefaultCheckEvery = 5 * time.Minute

// Options configure Run.
type Options struct {
	Policy    Policy
	ConfigDir string // recorded in the state; a state kept for another config dir is not reused
	Reaps     string // what a pass reaps, in words, for `claude-gc service status`
	StatePath string // where to keep the state; empty keeps it in memory only

	// Pass runs one reaping pass. An error means the pass could not run at
	// all; it is retried an hour later.
	Pass func() (Pass, error)
	Log  *slog.Logger
	// Trigger runs a pass at once on every receive, whatever the schedule
	// says. The daemon command wires SIGHUP to it.
	Trigger <-chan os.Signal
	// CheckEvery bounds the time between looks at the clock.
	CheckEvery time.Duration

	// The clock and the boot time, replaceable in tests.
	Now      func() time.Time
	BootTime func() (time.Time, error)
	After    func(time.Duration) <-chan time.Time
}

// Run runs passes on schedule until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	if o.Pass == nil {
		return errors.New("daemon: no pass function")
	}
	if o.Policy.Interval < time.Second {
		return errors.New("daemon: the interval must be at least a second")
	}
	if o.Policy.MinUptime < 0 {
		o.Policy.MinUptime = 0
	}
	if o.CheckEvery <= 0 {
		o.CheckEvery = DefaultCheckEvery
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.BootTime == nil {
		o.BootTime = proc.BootTime
	}
	if o.After == nil {
		o.After = time.After
	}

	r := &runner{Options: o, start: wall(o.Now())}
	r.loadState()
	for {
		if ctx.Err() != nil {
			return nil
		}
		now := wall(r.Now())
		boot := r.bootTime()
		next := r.state.Next(now, boot)
		if !now.Before(next) {
			r.runPass(now)
			continue
		}
		r.scheduled(now, boot, next)
		select {
		case <-ctx.Done():
			return nil
		case <-r.Trigger:
			r.Log.Info("received SIGHUP, running a pass now")
			r.runPass(wall(r.Now()))
		case <-r.After(min(next.Sub(now), r.CheckEvery)):
		}
	}
}

type runner struct {
	Options
	start      time.Time
	state      State
	bootWarned bool
}

func (r *runner) loadState() {
	var prev State
	if r.StatePath != "" {
		s, err := LoadState(r.StatePath)
		if err != nil {
			r.Log.Warn("ignoring an unreadable state file", "path", r.StatePath, "error", err)
		} else {
			prev = s
		}
	}
	r.state = State{
		PID:              os.Getpid(),
		ConfigDir:        r.ConfigDir,
		Reaps:            r.Reaps,
		IntervalSeconds:  int64(r.Policy.Interval / time.Second),
		MinUptimeSeconds: int64(r.Policy.MinUptime / time.Second),
	}
	if prev.ConfigDir == r.ConfigDir {
		r.state.LastPass = prev.LastPass
		r.state.LastError = prev.LastError
	}
	r.save()
}

// bootTime returns the boot time. If it cannot be read, the daemon's own
// start time stands in for it: the service starts at login, so the uptime
// rule still holds passes back after a restart.
func (r *runner) bootTime() time.Time {
	boot, err := r.BootTime()
	if err != nil {
		if !r.bootWarned {
			r.Log.Warn("cannot read the boot time; counting uptime from the daemon's start", "error", err)
			r.bootWarned = true
		}
		return r.start
	}
	return wall(boot)
}

// scheduled records and logs the next pass when it changes.
func (r *runner) scheduled(now, boot, next time.Time) {
	if !r.state.NextPass.IsZero() && absDiff(next, r.state.NextPass) < time.Minute {
		return
	}
	r.state.Boot = boot
	r.state.NextPass = next
	r.save()
	attrs := []any{
		"at", next.Local().Format("2006-01-02 15:04"),
		"in", humanize.Duration(next.Sub(now)),
		"uptime", humanize.Duration(now.Sub(boot)),
	}
	switch {
	case r.state.Policy().WaitingForUptime(boot, next):
		attrs = append(attrs, "reason", "waiting for "+humanize.Span(r.Policy.MinUptime)+" of uptime")
	case r.state.FailedSinceLastPass() != nil:
		attrs = append(attrs, "reason", "retrying after a failed pass")
	}
	r.Log.Info("next pass scheduled", attrs...)
}

func (r *runner) runPass(now time.Time) {
	p, err := r.Pass()
	if err != nil {
		r.state.LastError = &Failure{At: now, Error: err.Error()}
		r.save()
		r.Log.Error("pass failed", "error", err, "retry_in", humanize.Duration(min(retryAfterFailure, r.Policy.Interval)))
		return
	}
	p.At = now
	r.state.LastPass = &p
	r.state.LastError = nil
	r.save()
	r.Log.Info("pass complete",
		"sessions", p.Sessions,
		"reaped", p.Reaped,
		"freed", humanize.KB(p.FreedKB),
		"forced", p.Forced,
		"removed_files", p.Removed,
		"errors", p.Errors)
}

func (r *runner) save() {
	if r.StatePath == "" {
		return
	}
	if err := SaveState(r.StatePath, r.state); err != nil {
		r.Log.Warn("could not save state", "path", r.StatePath, "error", err)
	}
}

// wall strips the monotonic clock reading so comparisons use wall-clock
// time. Monotonic time stops while the computer sleeps; the schedule must not.
func wall(t time.Time) time.Time { return t.Round(0) }

func absDiff(a, b time.Time) time.Duration {
	if d := a.Sub(b); d >= 0 {
		return d
	}
	return b.Sub(a)
}
