package reaper

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mateusclira/claude-gc/internal/claude"
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/proc"
)

var now = time.Date(2026, 11, 27, 14, 0, 0, 0, time.UTC)

const claudeBin = "/Users/me/.vscode/extensions/anthropic.claude-code-2.1.259-darwin-arm64/resources/native-binary/claude --verbose"

type fixture struct {
	t          *testing.T
	configDir  string
	table      proc.Table
	transcript map[string]time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cfg := t.TempDir()
	if err := os.MkdirAll(claude.SessionsDir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, configDir: cfg, table: proc.Table{}, transcript: map[string]time.Time{}}
}

// process adds a live process started at start.
func (f *fixture) process(pid, ppid int, start time.Time, args string) {
	f.table[pid] = proc.Process{PID: pid, PPID: ppid, RSSKB: 200 * 1024, Start: start, Args: args}
}

// record writes a session record like Claude Code does.
func (f *fixture) record(pid int, entrypoint string, started time.Time, procStart string) claude.Record {
	f.t.Helper()
	rec := claude.Record{
		SessionID:  "sid-" + strconv.Itoa(pid),
		CWD:        "/home/me/app",
		StartedAt:  started.UnixMilli(),
		ProcStart:  procStart,
		Entrypoint: entrypoint,
		Name:       "app-" + strconv.Itoa(pid),
		Kind:       "interactive",
	}
	data, _ := json.Marshal(rec)
	dir := claude.SessionsDir(f.configDir)
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), data, 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".abc123.key"), []byte("{}"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	rec.PID = pid
	return rec
}

func (f *fixture) options() Options {
	return Options{
		ConfigDir:   f.configDir,
		MaxIdle:     3 * humanize.Day,
		Now:         now,
		SelfPID:     1,
		Snapshot:    func() (proc.Table, error) { return f.table, nil },
		Transcripts: func(string) (map[string]time.Time, error) { return f.transcript, nil },
		SocketDirs:  func() []string { return nil },
	}
}

func lstart(t time.Time) string { return t.UTC().Format(proc.StartLayout) }

func find(t *testing.T, rep *Report, pid int) Session {
	t.Helper()
	for _, s := range rep.Sessions {
		if s.Record.PID == pid {
			return s
		}
	}
	t.Fatalf("pid %d not in report", pid)
	return Session{}
}

func TestScanDecisions(t *testing.T) {
	f := newFixture(t)
	hostStart := now.Add(-82 * humanize.Day)
	f.process(1797, 1, hostStart, "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin)")

	// 100: dead process, record left behind.
	f.record(100, "claude-vscode", now.Add(-10*humanize.Day), lstart(now.Add(-10*humanize.Day)))

	// 101: pid recycled by another program.
	f.process(101, 1, now.Add(-time.Hour), "/usr/bin/python3 server.py")
	f.record(101, "claude-vscode", now.Add(-20*humanize.Day), lstart(now.Add(-20*humanize.Day)))

	// 102: claude process whose start time does not match the record.
	f.process(102, 1797, now.Add(-time.Hour), claudeBin)
	f.record(102, "claude-vscode", now.Add(-20*humanize.Day), lstart(now.Add(-20*humanize.Day)))

	// 103: old record without procStart; startedAt matches the process.
	f.process(103, 1797, now.Add(-30*humanize.Day).Add(-2*time.Second), claudeBin)
	f.record(103, "claude-vscode", now.Add(-30*humanize.Day), "")

	// 104: old record without procStart; startedAt does not match.
	f.process(104, 1797, now.Add(-time.Hour), claudeBin)
	f.record(104, "claude-vscode", now.Add(-30*humanize.Day), "")

	// 105: the session running claude-gc itself (an ancestor of SelfPID).
	f.process(105, 1797, now.Add(-9*humanize.Day), claudeBin)
	f.process(106, 105, now.Add(-time.Minute), "/bin/zsh")
	f.record(105, "claude-vscode", now.Add(-9*humanize.Day), lstart(now.Add(-9*humanize.Day)))

	// 107: terminal session, idle for ages, but not an eligible entrypoint.
	f.process(107, 1, now.Add(-9*humanize.Day), "claude")
	f.record(107, "cli", now.Add(-9*humanize.Day), lstart(now.Add(-9*humanize.Day)))

	// 108: recently active transcript.
	f.process(108, 1797, now.Add(-5*humanize.Day), claudeBin)
	f.record(108, "claude-vscode", now.Add(-5*humanize.Day), lstart(now.Add(-5*humanize.Day)))
	f.transcript["sid-108"] = now.Add(-2 * time.Hour)

	// 109: transcript idle beyond the threshold.
	f.process(109, 1797, now.Add(-5*humanize.Day), claudeBin)
	f.record(109, "claude-vscode", now.Add(-5*humanize.Day), lstart(now.Add(-5*humanize.Day)))
	f.transcript["sid-109"] = now.Add(-89 * time.Hour)

	// 110: no transcript, started minutes ago.
	f.process(110, 1797, now.Add(-3*time.Minute), claudeBin)
	f.record(110, "claude-vscode", now.Add(-3*time.Minute), lstart(now.Add(-3*time.Minute)))

	// 111: no transcript, started long ago (transcript garbage-collected).
	f.process(111, 1797, now.Add(-65*humanize.Day), claudeBin)
	f.record(111, "claude-vscode", now.Add(-65*humanize.Day), lstart(now.Add(-65*humanize.Day)))

	// 112: claude process with no record at all.
	f.process(112, 1797, now.Add(-40*humanize.Day), claudeBin)

	// 113: process start time could not be parsed.
	f.process(113, 1797, time.Time{}, claudeBin)
	f.record(113, "claude-vscode", now.Add(-40*humanize.Day), lstart(now.Add(-40*humanize.Day)))

	opts := f.options()
	opts.SelfPID = 106
	rep, err := Scan(opts)
	if err != nil {
		t.Fatal(err)
	}

	want := map[int]struct {
		action Action
		reason string
	}{
		100: {Clean, "process exited"},
		101: {Clean, "pid reused by python3"},
		102: {Skip, "start time does not match record"},
		103: {Reap, "no transcript, started 30d ago"},
		104: {Skip, "start time does not match record"},
		105: {Skip, "current session"},
		107: {Skip, "entrypoint cli"},
		108: {Keep, "idle 2h"},
		109: {Reap, "idle 3d 17h"},
		110: {Keep, "no transcript, started 3m ago"},
		111: {Reap, "no transcript, started 65d ago"},
		113: {Skip, "cannot verify process identity"},
	}
	if len(rep.Sessions) != len(want) {
		t.Fatalf("got %d sessions, want %d", len(rep.Sessions), len(want))
	}
	for pid, w := range want {
		s := find(t, rep, pid)
		if s.Action != w.action || s.Reason != w.reason {
			t.Errorf("pid %d: got %s (%s), want %s (%s)", pid, s.Action, s.Reason, w.action, w.reason)
		}
	}
	for _, pid := range []int{103, 108, 109, 110, 111} {
		if !find(t, rep, pid).Verified {
			t.Errorf("pid %d should be verified", pid)
		}
	}
	if got := find(t, rep, 109).LastActivity; !got.Equal(now.Add(-89 * time.Hour)) {
		t.Errorf("last activity = %v", got)
	}

	// Reaped and cleaned sessions carry their leftover files.
	if files := find(t, rep, 111).Files; len(files) != 2 {
		t.Errorf("files for 111 = %v, want record and key", files)
	}
	if files := find(t, rep, 100).Files; len(files) != 2 {
		t.Errorf("files for 100 = %v", files)
	}
	if files := find(t, rep, 108).Files; len(files) != 0 {
		t.Errorf("kept session must not list files: %v", files)
	}

	if rep.Count(Reap) != 3 || rep.Count(Keep) != 2 || rep.Count(Skip) != 5 || rep.Count(Clean) != 2 {
		t.Errorf("counts: reap=%d keep=%d skip=%d clean=%d", rep.Count(Reap), rep.Count(Keep), rep.Count(Skip), rep.Count(Clean))
	}
	if got := rep.ReapableKB(); got != 3*200*1024 {
		t.Errorf("ReapableKB = %d", got)
	}
	if got := rep.ResidentKB(); got != 10*200*1024 {
		t.Errorf("ResidentKB = %d (dead sessions must not count)", got)
	}
	if len(rep.Unknown) != 1 || rep.Unknown[0].PID != 112 {
		t.Errorf("unknown = %+v", rep.Unknown)
	}
	// Most idle first.
	if rep.Sessions[0].Record.PID != 111 {
		t.Errorf("first session = %d, want 111 (most idle)", rep.Sessions[0].Record.PID)
	}
}

func TestScanEntrypointWildcard(t *testing.T) {
	f := newFixture(t)
	f.process(107, 1, now.Add(-9*humanize.Day), "claude")
	f.record(107, "cli", now.Add(-9*humanize.Day), lstart(now.Add(-9*humanize.Day)))
	opts := f.options()
	opts.Entrypoints = []string{"*"}
	rep, err := Scan(opts)
	if err != nil {
		t.Fatal(err)
	}
	if s := find(t, rep, 107); s.Action != Reap {
		t.Errorf("wildcard: got %s (%s)", s.Action, s.Reason)
	}
	opts.Entrypoints = []string{"claude-vscode", "cli"}
	rep, _ = Scan(opts)
	if s := find(t, rep, 107); s.Action != Reap {
		t.Errorf("explicit list: got %s (%s)", s.Action, s.Reason)
	}
}

func TestScanLocalTimeRecord(t *testing.T) {
	// A record whose procStart was captured in local time must still verify.
	f := newFixture(t)
	start := now.Add(-10 * humanize.Day)
	f.process(200, 1, start, claudeBin)
	f.record(200, "claude-vscode", start, start.In(time.Local).Format(proc.StartLayout))
	rep, err := Scan(f.options())
	if err != nil {
		t.Fatal(err)
	}
	if s := find(t, rep, 200); s.Action != Reap || !s.Verified {
		t.Errorf("got %s (%s) verified=%v", s.Action, s.Reason, s.Verified)
	}
}

func TestScanStaleSockets(t *testing.T) {
	f := newFixture(t)
	// Unix socket paths are limited to about 100 bytes; t.TempDir() is longer.
	tmp, err := os.MkdirTemp("/tmp", "claude-gc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "cc-socks")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listen := func(name string) {
		l, err := net.Listen("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
	}
	listen("300.sock") // live claude process with a record: not stale
	listen("301.sock") // dead process: stale
	listen("302.sock") // pid now belongs to another program: stale
	listen("303.sock") // reaped session's own socket: claimed by the session, not listed twice
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.process(300, 1, now.Add(-time.Hour), claudeBin)
	f.record(300, "claude-vscode", now.Add(-time.Hour), lstart(now.Add(-time.Hour)))
	f.process(302, 1, now.Add(-time.Hour), "/usr/bin/python3")
	f.process(303, 1, now.Add(-10*humanize.Day), claudeBin)
	rec := f.record(303, "claude-vscode", now.Add(-10*humanize.Day), lstart(now.Add(-10*humanize.Day)))
	rec.SocketPath = filepath.Join(dir, "303.sock")
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(claude.SessionsDir(f.configDir), "303.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	opts := f.options()
	opts.SocketDirs = func() []string { return []string{dir} }
	rep, err := Scan(opts)
	if err != nil {
		t.Fatal(err)
	}
	wantStale := []string{filepath.Join(dir, "301.sock"), filepath.Join(dir, "302.sock")}
	if strings.Join(rep.StaleSockets, ",") != strings.Join(wantStale, ",") {
		t.Errorf("stale sockets = %v, want %v", rep.StaleSockets, wantStale)
	}
	s := find(t, rep, 303)
	if s.Action != Reap || len(s.Files) != 3 || s.Files[2] != filepath.Join(dir, "303.sock") {
		t.Errorf("303: action=%s files=%v", s.Action, s.Files)
	}
}

func TestScanTranscriptIndexFailure(t *testing.T) {
	f := newFixture(t)
	opts := f.options()
	opts.Transcripts = func(string) (map[string]time.Time, error) { return nil, errors.New("disk on fire") }
	if _, err := Scan(opts); err == nil {
		t.Fatal("expected error when transcripts cannot be indexed")
	}
	opts = f.options()
	opts.Snapshot = func() (proc.Table, error) { return nil, errors.New("no ps") }
	if _, err := Scan(opts); err == nil {
		t.Fatal("expected error when ps fails")
	}
}

func TestApplyDryRun(t *testing.T) {
	f := newFixture(t)
	f.process(400, 1, now.Add(-10*humanize.Day), claudeBin)
	f.record(400, "claude-vscode", now.Add(-10*humanize.Day), lstart(now.Add(-10*humanize.Day)))
	f.record(401, "claude-vscode", now.Add(-10*humanize.Day), lstart(now.Add(-10*humanize.Day)))
	rep, err := Scan(f.options())
	if err != nil {
		t.Fatal(err)
	}
	terminated := false
	res := Apply(rep, ApplyOptions{
		DryRun:    true,
		Terminate: func(int, time.Duration) (bool, error) { terminated = true; return false, nil },
		Remove:    func(string) error { t.Error("remove called in dry run"); return nil },
	})
	if terminated {
		t.Error("terminate called in dry run")
	}
	if !res.DryRun || len(res.Reaped) != 1 || len(res.Cleaned) != 1 || len(res.Removed) != 4 {
		t.Errorf("result = %+v", res)
	}
	if res.FreedKB != 200*1024 {
		t.Errorf("FreedKB = %d", res.FreedKB)
	}
	if _, err := os.Stat(filepath.Join(claude.SessionsDir(f.configDir), "401.json")); err != nil {
		t.Error("dry run removed a file")
	}
}

func TestApplyIdentityRecheck(t *testing.T) {
	f := newFixture(t)
	start := now.Add(-10 * humanize.Day)
	f.process(500, 1, start, claudeBin)
	f.record(500, "claude-vscode", start, lstart(start))
	rep, err := Scan(f.options())
	if err != nil {
		t.Fatal(err)
	}
	terminated := 0
	var removed []string
	remove := func(path string) error {
		if !strings.HasPrefix(path, f.configDir) {
			t.Fatalf("attempt to remove a file outside the test config dir: %s", path)
		}
		removed = append(removed, path)
		return nil
	}
	res := Apply(rep, ApplyOptions{
		StartTime: func(int) (time.Time, bool, error) { return now.Add(-time.Minute), true, nil },
		Terminate: func(int, time.Duration) (bool, error) { terminated++; return false, nil },
		Remove:    remove,
	})
	if terminated != 0 || len(res.Errors) != 1 || len(res.Reaped) != 0 || len(removed) != 0 {
		t.Errorf("recycled pid must not be signalled: %+v", res)
	}

	// Process vanished between scan and apply: just clean up.
	res = Apply(rep, ApplyOptions{
		StartTime: func(int) (time.Time, bool, error) { return time.Time{}, false, nil },
		Terminate: func(int, time.Duration) (bool, error) { terminated++; return false, nil },
		Remove:    remove,
	})
	if terminated != 0 || len(res.Cleaned) != 1 || len(res.Removed) != 2 || len(removed) != 2 {
		t.Errorf("vanished pid: %+v", res)
	}
}

// TestApplyLive reaps a real child process through the real ps and kill.
func TestApplyLive(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	start, ok, err := proc.StartTime(pid)
	if err != nil || !ok {
		t.Fatalf("StartTime: %v %v", ok, err)
	}
	f := newFixture(t)
	f.record(pid, "claude-vscode", now.Add(-10*humanize.Day), lstart(start))

	opts := f.options()
	opts.Snapshot = proc.Snapshot
	opts.Now = time.Now()
	opts.IsClaude = func(args string) bool { return strings.Contains(args, "sleep") }
	rep, err := Scan(opts)
	if err != nil {
		t.Fatal(err)
	}
	s := find(t, rep, pid)
	if s.Action != Reap {
		t.Fatalf("got %s (%s), want reap", s.Action, s.Reason)
	}
	if len(rep.StaleSockets) != 0 {
		t.Fatalf("test must not see real socket dirs: %v", rep.StaleSockets)
	}
	res := Apply(rep, ApplyOptions{
		Grace: 3 * time.Second,
		Remove: func(path string) error {
			if !strings.HasPrefix(path, f.configDir) {
				t.Fatalf("attempt to remove a file outside the test config dir: %s", path)
			}
			return os.Remove(path)
		},
	})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if len(res.Reaped) != 1 || res.Forced != 0 {
		t.Errorf("result = %+v", res)
	}
	select {
	case err := <-waited:
		t.Logf("child exited: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	if proc.Alive(pid) {
		t.Error("child still alive")
	}
	if entries, _ := os.ReadDir(claude.SessionsDir(f.configDir)); len(entries) != 0 {
		t.Errorf("leftover files remain: %v", entries)
	}
}
