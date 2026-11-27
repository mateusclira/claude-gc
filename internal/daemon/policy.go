// Package daemon runs reaping passes on a schedule and records what it did.
//
// The schedule works on wall-clock time. Time the computer spends asleep
// counts, so a pass that comes due overnight runs shortly after it wakes.
package daemon

import (
	"strings"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
)

// The default schedule: once a day, never in the first day after a restart.
const (
	DefaultInterval  = 24 * time.Hour
	DefaultMinUptime = 24 * time.Hour
)

// Policy says when passes run.
type Policy struct {
	// Interval is the time between passes.
	Interval time.Duration
	// MinUptime holds passes back until the computer has been up this long.
	// A restart ends every Claude Code session, so right after one there is
	// nothing to collect. Zero disables the rule.
	MinUptime time.Duration
}

// Next returns when the next pass is due, given when the computer booted and
// when the last pass ran (zero for never). All times are wall-clock times.
//
// The first pass after a restart is due once uptime reaches MinUptime, and
// each later one Interval after the one before. A last pass from before the
// restart does not count.
func (p Policy) Next(now, boot, last time.Time) time.Time {
	due := boot.Add(p.MinUptime)
	if last.After(now) {
		last = now // the clock was set back; do not wait for a time that already passed
	}
	if last.After(boot) {
		if next := last.Add(p.Interval); next.After(due) {
			due = next
		}
	}
	return due
}

// WaitingForUptime reports whether next is held back by the uptime rule
// rather than by the interval.
func (p Policy) WaitingForUptime(boot, next time.Time) bool {
	return p.MinUptime > 0 && next.Equal(boot.Add(p.MinUptime))
}

// Describe renders the policy for people, for example
// "once a day, after 24 hours of uptime".
func (p Policy) Describe() string {
	var b strings.Builder
	if p.Interval == 24*time.Hour {
		b.WriteString("once a day")
	} else {
		b.WriteString("every " + humanize.Span(p.Interval))
	}
	if p.MinUptime > 0 {
		b.WriteString(", after " + humanize.Span(p.MinUptime) + " of uptime")
	}
	return b.String()
}
