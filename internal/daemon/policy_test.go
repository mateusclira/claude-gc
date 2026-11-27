package daemon

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func TestPolicyNext(t *testing.T) {
	day := 24 * time.Hour
	def := Policy{Interval: day, MinUptime: day}
	cases := []struct {
		name   string
		policy Policy
		now    time.Time
		boot   time.Time
		last   time.Time
		want   time.Time
	}{
		{"just booted, never ran", def, t0.Add(time.Hour), t0, time.Time{}, t0.Add(day)},
		{"up for weeks, never ran", def, t0, t0.Add(-82 * day), time.Time{}, t0.Add(-81 * day)},
		{"ran two hours ago", def, t0, t0.Add(-82 * day), t0.Add(-2 * time.Hour), t0.Add(22 * time.Hour)},
		{"last pass was before the restart", def, t0, t0.Add(-time.Hour), t0.Add(-3 * day), t0.Add(23 * time.Hour)},
		{"uptime rule disabled", Policy{Interval: day}, t0, t0.Add(-time.Minute), time.Time{}, t0.Add(-time.Minute)},
		{"clock set back", def, t0, t0.Add(-82 * day), t0.Add(5 * day), t0.Add(day)},
		{"shorter interval than uptime rule", Policy{Interval: 12 * time.Hour, MinUptime: day}, t0.Add(30 * time.Hour), t0, t0.Add(day), t0.Add(36 * time.Hour)},
		{"interval shorter, pass right after boot", Policy{Interval: 12 * time.Hour, MinUptime: day}, t0.Add(time.Hour), t0, t0.Add(30 * time.Minute), t0.Add(day)},
	}
	for _, c := range cases {
		if got := c.policy.Next(c.now, c.boot, c.last); !got.Equal(c.want) {
			t.Errorf("%s: Next = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPolicyWaitingForUptime(t *testing.T) {
	p := Policy{Interval: 24 * time.Hour, MinUptime: 24 * time.Hour}
	if !p.WaitingForUptime(t0, t0.Add(24*time.Hour)) {
		t.Error("next at boot+24h should be waiting for uptime")
	}
	if p.WaitingForUptime(t0, t0.Add(30*time.Hour)) {
		t.Error("next after boot+24h is driven by the interval")
	}
	if (Policy{Interval: time.Hour}).WaitingForUptime(t0, t0) {
		t.Error("no uptime rule, never waiting for uptime")
	}
}

func TestPolicyDescribe(t *testing.T) {
	cases := map[Policy]string{
		{Interval: 24 * time.Hour, MinUptime: 24 * time.Hour}:     "once a day, after 24 hours of uptime",
		{Interval: 12 * time.Hour}:                                "every 12 hours",
		{Interval: 30 * time.Minute}:                              "every 30m",
		{Interval: 7 * 24 * time.Hour, MinUptime: 36 * time.Hour}: "every 7d, after 36 hours of uptime",
	}
	for p, want := range cases {
		if got := p.Describe(); got != want {
			t.Errorf("Describe(%+v) = %q, want %q", p, got, want)
		}
	}
}
