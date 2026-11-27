package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const day = 24 * time.Hour

// sim drives Run with a fake clock. Every wait advances the wall clock by the
// requested nap, plus the length of a simulated sleep when the clock crosses
// sleepAt, which is what a real computer looks like from the inside: timers
// pause while it sleeps, and the wall clock jumps when it wakes.
type sim struct {
	t       *testing.T
	now     time.Time
	boot    time.Time
	bootErr error
	until   time.Time
	cancel  context.CancelFunc

	sleepAt   time.Time
	sleepFor  time.Duration
	slept     bool
	holdFirst bool // the first wait never fires, so a pending trigger wins

	waits    int
	maxNap   time.Duration
	attempts int
	fail     map[int]bool // 1-based pass attempts that fail
	passes   []time.Time
}

func (s *sim) after(d time.Duration) <-chan time.Time {
	s.waits++
	if d <= 0 {
		s.t.Fatalf("non-positive nap %v", d)
	}
	if s.holdFirst && s.waits == 1 {
		return nil
	}
	s.maxNap = max(s.maxNap, d)
	s.now = s.now.Add(d)
	if !s.slept && !s.sleepAt.IsZero() && !s.now.Before(s.sleepAt) {
		s.now = s.now.Add(s.sleepFor)
		s.slept = true
	}
	if !s.now.Before(s.until) {
		s.cancel()
		return nil
	}
	ch := make(chan time.Time, 1)
	ch <- s.now
	return ch
}

func (s *sim) pass() (Pass, error) {
	s.attempts++
	if s.fail[s.attempts] {
		return Pass{}, errors.New("listing processes: ps failed")
	}
	s.passes = append(s.passes, s.now)
	return Pass{Sessions: 3, Reaped: 1, FreedKB: 200 * 1024}, nil
}

func (s *sim) run(o Options) {
	s.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.cancel = cancel
	if o.Policy == (Policy{}) {
		o.Policy = Policy{Interval: DefaultInterval, MinUptime: DefaultMinUptime}
	}
	o.Now = func() time.Time { return s.now }
	o.BootTime = func() (time.Time, error) { return s.boot, s.bootErr }
	o.After = s.after
	o.Pass = s.pass
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		s.t.Fatal("Run did not stop")
	}
}

func (s *sim) expect(want ...time.Time) {
	s.t.Helper()
	if len(s.passes) != len(want) {
		s.t.Fatalf("got %d passes %v, want %d %v", len(s.passes), s.passes, len(want), want)
	}
	for i := range want {
		if !s.passes[i].Equal(want[i]) {
			s.t.Errorf("pass %d at %v, want %v", i+1, s.passes[i], want[i])
		}
	}
}

func TestRunWaitsForUptimeAfterBoot(t *testing.T) {
	s := &sim{t: t, boot: t0, now: t0.Add(2 * time.Minute), until: t0.Add(5*day + time.Hour)}
	s.run(Options{})
	s.expect(t0.Add(day), t0.Add(2*day), t0.Add(3*day), t0.Add(4*day), t0.Add(5*day))
	if s.maxNap > DefaultCheckEvery {
		t.Errorf("slept %v between checks, more than %v", s.maxNap, DefaultCheckEvery)
	}
}

func TestRunLongUptimeRunsAtOnce(t *testing.T) {
	s := &sim{t: t, boot: t0.Add(-82 * day), now: t0, until: t0.Add(2*day + time.Hour)}
	s.run(Options{})
	s.expect(t0, t0.Add(day), t0.Add(2*day))
}

func TestRunRestartKeepsSchedule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	prev := State{ConfigDir: "/cfg", LastPass: &Pass{At: t0.Add(-2 * time.Hour)}}
	if err := SaveState(path, prev); err != nil {
		t.Fatal(err)
	}
	s := &sim{t: t, boot: t0.Add(-82 * day), now: t0, until: t0.Add(day)}
	s.run(Options{ConfigDir: "/cfg", StatePath: path})
	s.expect(t0.Add(22 * time.Hour))
}

func TestRunIgnoresStateOfAnotherConfigDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	prev := State{ConfigDir: "/other", LastPass: &Pass{At: t0.Add(-2 * time.Hour)}}
	if err := SaveState(path, prev); err != nil {
		t.Fatal(err)
	}
	s := &sim{t: t, boot: t0.Add(-82 * day), now: t0, until: t0.Add(time.Hour)}
	s.run(Options{ConfigDir: "/cfg", StatePath: path})
	s.expect(t0)
}

func TestRunAfterRebootIgnoresOldPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	prev := State{ConfigDir: "/cfg", LastPass: &Pass{At: t0.Add(-3 * day)}}
	if err := SaveState(path, prev); err != nil {
		t.Fatal(err)
	}
	boot := t0.Add(-time.Minute)
	s := &sim{t: t, boot: boot, now: t0, until: boot.Add(day + time.Hour)}
	s.run(Options{ConfigDir: "/cfg", StatePath: path})
	s.expect(boot.Add(day))
}

func TestRunCatchesUpAfterSleep(t *testing.T) {
	// The computer sleeps from 20h to 30h of uptime, across the 24h mark.
	s := &sim{t: t, boot: t0, now: t0.Add(time.Minute), until: t0.Add(2*day + 7*time.Hour),
		sleepAt: t0.Add(20 * time.Hour), sleepFor: 10 * time.Hour}
	s.run(Options{})
	if len(s.passes) != 2 {
		t.Fatalf("passes = %v", s.passes)
	}
	wake := t0.Add(30 * time.Hour)
	if s.passes[0].Before(wake) || s.passes[0].After(wake.Add(DefaultCheckEvery)) {
		t.Errorf("pass after waking at %v, want within %v of %v", s.passes[0], DefaultCheckEvery, wake)
	}
	if got := s.passes[1].Sub(s.passes[0]); got != day {
		t.Errorf("second pass %v after the first, want 24h", got)
	}
}

func TestRunWithoutUptimeRule(t *testing.T) {
	s := &sim{t: t, boot: t0, now: t0.Add(time.Minute), until: t0.Add(day + time.Hour)}
	s.run(Options{Policy: Policy{Interval: day}})
	s.expect(t0.Add(time.Minute), t0.Add(day+time.Minute))
}

func TestRunTriggerIgnoresSchedule(t *testing.T) {
	trigger := make(chan os.Signal, 1)
	trigger <- syscall.SIGHUP
	s := &sim{t: t, boot: t0, now: t0.Add(time.Minute), until: t0.Add(time.Minute + time.Second), holdFirst: true}
	s.run(Options{Trigger: trigger})
	s.expect(t0.Add(time.Minute))
}

func TestRunRetriesFailedPassAfterAnHour(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &sim{t: t, boot: t0.Add(-82 * day), now: t0, until: t0.Add(day + 2*time.Hour), fail: map[int]bool{1: true}}
	s.run(Options{StatePath: path})
	if s.attempts != 3 {
		t.Errorf("attempts = %d, want 3 (fail, retry, next day)", s.attempts)
	}
	s.expect(t0.Add(time.Hour), t0.Add(day+time.Hour))
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastError != nil {
		t.Errorf("a successful pass must clear the error: %+v", st.LastError)
	}
}

func TestRunWithoutBootTimeCountsFromStart(t *testing.T) {
	s := &sim{t: t, bootErr: errors.New("no sysctl"), now: t0, until: t0.Add(day + time.Hour)}
	s.run(Options{})
	s.expect(t0.Add(day))
}

func TestRunRecordsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	boot := t0.Add(-82 * day)
	s := &sim{t: t, boot: boot, now: t0, until: t0.Add(time.Hour)}
	s.run(Options{ConfigDir: "/cfg", StatePath: path, Reaps: "claude-vscode sessions idle for longer than 3d"})
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.PID != os.Getpid() || st.ConfigDir != "/cfg" || st.Reaps == "" {
		t.Errorf("identity: %+v", st)
	}
	if st.IntervalSeconds != 86400 || st.MinUptimeSeconds != 86400 {
		t.Errorf("policy: %+v", st.Policy())
	}
	if st.LastPass == nil || !st.LastPass.At.Equal(t0) || st.LastPass.Reaped != 1 {
		t.Errorf("last pass: %+v", st.LastPass)
	}
	if !st.NextPass.Equal(t0.Add(day)) || !st.Boot.Equal(boot) {
		t.Errorf("next pass %v, boot %v", st.NextPass, st.Boot)
	}
}

func TestRunRejectsBadOptions(t *testing.T) {
	ctx := context.Background()
	if err := Run(ctx, Options{Policy: Policy{Interval: day}}); err == nil {
		t.Error("missing pass function accepted")
	}
	pass := func() (Pass, error) { return Pass{}, nil }
	if err := Run(ctx, Options{Pass: pass}); err == nil {
		t.Error("zero interval accepted")
	}
}
