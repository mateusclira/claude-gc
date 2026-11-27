package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	s, err := LoadState(path)
	if err != nil || s.LastPass != nil || s.PID != 0 {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	want := State{
		PID:              42,
		ConfigDir:        "/home/me/.claude",
		Reaps:            "claude-vscode sessions idle for longer than 3d",
		IntervalSeconds:  86400,
		MinUptimeSeconds: 86400,
		Boot:             t0,
		NextPass:         t0.Add(48 * time.Hour),
		LastPass:         &Pass{At: t0.Add(24 * time.Hour), Sessions: 42, Reaped: 38, FreedKB: 7932080, Removed: 76},
	}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 42 || got.Reaps != want.Reaps || !got.NextPass.Equal(want.NextPass) || got.LastPass == nil || got.LastPass.Reaped != 38 {
		t.Errorf("round trip: %+v", got)
	}
	if got.Policy() != (Policy{Interval: 24 * time.Hour, MinUptime: 24 * time.Hour}) {
		t.Errorf("policy = %+v", got.Policy())
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temporary file left behind")
	}

	// Zero times and absent passes are left out of the file.
	if err := SaveState(path, State{PID: 1}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, key := range []string{"nextPass", "boot", "lastPass", "lastError"} {
		if strings.Contains(string(data), key) {
			t.Errorf("empty state contains %q:\n%s", key, data)
		}
	}

	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Error("corrupt state accepted")
	}
}

func TestStateNextRetriesFailures(t *testing.T) {
	boot := t0.Add(-82 * 24 * time.Hour)
	s := State{IntervalSeconds: 86400, MinUptimeSeconds: 86400}

	s.LastError = &Failure{At: t0, Error: "ps failed"}
	if got := s.Next(t0, boot); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("never ran, failed at t0: next = %v, want t0+1h", got)
	}

	s.LastPass = &Pass{At: t0.Add(-time.Hour)}
	if got := s.Next(t0, boot); !got.Equal(t0.Add(23 * time.Hour)) {
		t.Errorf("a failure after a recent pass must not pull the next pass earlier: next = %v, want t0+23h", got)
	}

	s.LastPass = &Pass{At: t0.Add(-30 * time.Hour)}
	if got := s.Next(t0, boot); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("overdue pass that failed: next = %v, want t0+1h", got)
	}

	s.LastPass = &Pass{At: t0.Add(time.Minute)}
	if s.FailedSinceLastPass() != nil {
		t.Error("a pass after the failure clears it")
	}

	short := State{IntervalSeconds: 1800, LastError: &Failure{At: t0}}
	if got := short.Next(t0, boot); !got.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("retry is capped by the interval: next = %v", got)
	}
}

func TestStatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "claude-gc", "state.json")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "claude-gc", "state.json")
	}
	if path != want {
		t.Errorf("StatePath = %q, want %q", path, want)
	}
}
