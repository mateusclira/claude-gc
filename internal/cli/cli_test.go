package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mateusclira/claude-gc/internal/daemon"
)

func newTestApp() (*app, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return &app{stdout: &out, stderr: &errb, build: BuildInfo{Version: "1.2.3", Commit: "abc", Date: "today"}}, &out, &errb
}

func TestDispatch(t *testing.T) {
	a, out, errb := newTestApp()
	if code := a.run(nil); code != 2 || !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("no args: code=%d stderr=%q", code, errb.String())
	}
	errb.Reset()
	if code := a.run([]string{"bogus"}); code != 2 || !strings.Contains(errb.String(), `unknown command "bogus"`) {
		t.Errorf("bogus: code=%d stderr=%q", code, errb.String())
	}
	if code := a.run([]string{"help"}); code != 0 || !strings.Contains(out.String(), "claude-gc <command>") {
		t.Errorf("help: code=%d", code)
	}
	out.Reset()
	if code := a.run([]string{"version"}); code != 0 || !strings.HasPrefix(out.String(), "claude-gc v1.2.3 (commit abc") {
		t.Errorf("version: code=%d out=%q", code, out.String())
	}
	errb.Reset()
	if code := a.run([]string{"service"}); code != 2 {
		t.Errorf("service without subcommand: code=%d", code)
	}
	errb.Reset()
	if code := a.run([]string{"service", "dance"}); code != 2 || !strings.Contains(errb.String(), "unknown service command") {
		t.Errorf("service dance: code=%d stderr=%q", code, errb.String())
	}
}

func TestFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"zero max-idle", []string{"status", "--max-idle", "0"}, nil, "--max-idle must be greater than zero"},
		{"unitless max-idle", []string{"status", "--max-idle", "3"}, nil, "missing unit"},
		{"bad min-uptime", []string{"daemon", "--min-uptime", "-1h"}, nil, `invalid value "-1h"`},
		{"bad min-uptime env", []string{"daemon"}, map[string]string{"CLAUDE_GC_MIN_UPTIME": "a while"}, "CLAUDE_GC_MIN_UPTIME"},
		{"interval flag on status", []string{"status", "--interval", "1h"}, nil, "flag provided but not defined: -interval"},
		{"bad max-idle", []string{"status", "--max-idle", "soon"}, nil, `invalid value "soon"`},
		{"bad env", []string{"status"}, map[string]string{"CLAUDE_GC_MAX_IDLE": "never"}, "CLAUDE_GC_MAX_IDLE"},
		{"empty entrypoints", []string{"status", "--entrypoints", " , "}, nil, "--entrypoints must name"},
		{"positional", []string{"status", "extra"}, nil, `unexpected argument "extra"`},
		{"short interval", []string{"daemon", "--interval", "5s"}, nil, "--interval must be at least 1m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			a, _, errb := newTestApp()
			if code := a.run(c.args); code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Errorf("stderr = %q, want it to contain %q", errb.String(), c.want)
			}
		})
	}
}

func TestEnvDefaults(t *testing.T) {
	t.Setenv("CLAUDE_GC_MAX_IDLE", "1d")
	t.Setenv("CLAUDE_GC_ENTRYPOINTS", "claude-vscode, cli")
	t.Setenv("CLAUDE_GC_GRACE", "9s")
	t.Setenv("CLAUDE_GC_INTERVAL", "2h")
	t.Setenv("CLAUDE_GC_MIN_UPTIME", "0")
	a, _, _ := newTestApp()
	var sf scanFlags
	fs := a.flagSet("test", &sf, "")
	sf.addScheduleFlags(fs)
	if err := fs.Parse([]string{"--grace", "1s"}); err != nil {
		t.Fatal(err)
	}
	if err := sf.resolve(fs); err != nil {
		t.Fatal(err)
	}
	if sf.maxIdle.d != 24*time.Hour {
		t.Errorf("max-idle from env = %v, want 24h", sf.maxIdle.d)
	}
	if got := strings.Join(sf.entrypointList(), "|"); got != "claude-vscode|cli" {
		t.Errorf("entrypoints from env = %q", got)
	}
	if got := sf.grace.String(); got != "1s" {
		t.Errorf("explicit flag must win over env: grace = %q", got)
	}
	if got := sf.interval.String(); got != "2h" {
		t.Errorf("interval from env = %q", got)
	}
	if sf.minUptime.d != 0 {
		t.Errorf("min-uptime from env = %v, want 0 (disabled)", sf.minUptime.d)
	}
	if got := sf.policy().Describe(); got != "every 2 hours" {
		t.Errorf("policy = %q", got)
	}
}

func TestScheduleDefaults(t *testing.T) {
	a, _, _ := newTestApp()
	var sf scanFlags
	fs := a.flagSet("test", &sf, "")
	sf.addScheduleFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := sf.resolve(fs); err != nil {
		t.Fatal(err)
	}
	if got := sf.policy().Describe(); got != "once a day, after 24 hours of uptime" {
		t.Errorf("default policy = %q", got)
	}
	if got := sf.reaps(); got != "claude-vscode sessions idle for longer than 3d" {
		t.Errorf("reaps = %q", got)
	}
	sf.entrypoints = "claude-vscode,*"
	if got := sf.reaps(); got != "all sessions idle for longer than 3d" {
		t.Errorf("reaps with wildcard = %q", got)
	}
}

func TestScheduleEnvIgnoredByStatus(t *testing.T) {
	// status and run have no schedule, so a schedule setting meant for the
	// daemon must not break them.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_GC_INTERVAL", "5s")
	t.Setenv("CLAUDE_GC_MIN_UPTIME", "never")
	a, _, errb := newTestApp()
	if code := a.run([]string{"status"}); code != 0 {
		t.Errorf("status: code=%d stderr=%s", code, errb.String())
	}
}

func TestScheduleRows(t *testing.T) {
	day := 24 * time.Hour
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	longBoot := now.Add(-82*day - 3*time.Hour)
	state := daemon.State{
		Reaps:            "claude-vscode sessions idle for longer than 3d",
		IntervalSeconds:  86400,
		MinUptimeSeconds: 86400,
	}
	text := func(rows [][2]string) string {
		var b strings.Builder
		printRows(&b, rows)
		return b.String()
	}

	// Freshly installed on a computer that has been up for weeks.
	got := text(scheduleRows(state, true, now, longBoot, nil))
	for _, want := range []string{
		"Schedule:   once a day, after 24 hours of uptime\n",
		"Reaps:      claude-vscode sessions idle for longer than 3d\n",
		"Uptime:     82d 3h\n",
		"Last pass:  none yet\n",
		"Next pass:  now\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fresh install output missing %q:\n%s", want, got)
		}
	}

	// Ran two hours ago.
	state.LastPass = &daemon.Pass{At: now.Add(-2 * time.Hour), Sessions: 42, Reaped: 38, FreedKB: 7932080}
	got = text(scheduleRows(state, true, now, longBoot, nil))
	for _, want := range []string{
		"Last pass:  2h ago, reaped 38 sessions and freed 7.6 GB\n",
		"Next pass:  in 22h (",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("after a pass, output missing %q:\n%s", want, got)
		}
	}

	// Restarted three hours ago; the last pass was before the restart.
	state.LastPass = &daemon.Pass{At: now.Add(-3 * day), Reaped: 0}
	boot := now.Add(-3 * time.Hour)
	got = text(scheduleRows(state, true, now, boot, nil))
	for _, want := range []string{
		"Uptime:     3h\n",
		"Last pass:  3d ago, nothing to reap, before the last restart\n",
		"Next pass:  in 21h (",
		"), once uptime reaches 24 hours\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("after a restart, output missing %q:\n%s", want, got)
		}
	}

	// A failed pass is reported, and a stopped daemon has no next pass.
	state.LastError = &daemon.Failure{At: now.Add(-time.Hour), Error: "listing processes: ps failed"}
	got = text(scheduleRows(state, false, now, boot, nil))
	if !strings.Contains(got, "Last error: 1h ago: listing processes: ps failed\n") {
		t.Errorf("missing last error:\n%s", got)
	}
	if strings.Contains(got, "Next pass") {
		t.Errorf("stopped daemon must not show a next pass:\n%s", got)
	}
}

func TestStatusAndDryRunOnEmptyConfigDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	a, out, errb := newTestApp()
	if code := a.run([]string{"status"}); code != 0 {
		t.Fatalf("status: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "No Claude Code sessions found") {
		t.Errorf("status output = %q", out.String())
	}

	a, out, errb = newTestApp()
	if code := a.run([]string{"status", "--json", "--config-dir", cfg}); code != 0 {
		t.Fatalf("status --json: code=%d stderr=%s", code, errb.String())
	}
	var rep reportJSON
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if rep.Summary.Sessions != 0 || rep.MaxIdleSeconds != 3*24*3600 || len(rep.Entrypoints) != 1 {
		t.Errorf("report = %+v", rep)
	}
	if rep.Sessions == nil || rep.Unknown == nil || rep.StaleSockets == nil {
		t.Error("JSON arrays must not be null")
	}

	a, out, errb = newTestApp()
	if code := a.run([]string{"run", "--dry-run"}); code != 0 {
		t.Fatalf("run --dry-run: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Dry run: nothing to reap among 0 sessions") {
		t.Errorf("dry run output = %q", out.String())
	}

	a, out, errb = newTestApp()
	if code := a.run([]string{"run", "-n", "--json"}); code != 0 {
		t.Fatalf("run -n --json: code=%d stderr=%s", code, errb.String())
	}
	var res resultJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if !res.DryRun || len(res.Reaped) != 0 || res.Errors == nil {
		t.Errorf("result = %+v", res)
	}
}

func TestShortenHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if got := shortenHome(filepath.Join(home, "code", "x")); got != "~/code/x" {
		t.Errorf("shortenHome = %q", got)
	}
	if got := shortenHome(home); got != "~" {
		t.Errorf("shortenHome(home) = %q", got)
	}
	if got := shortenHome(home + "2/x"); got != home+"2/x" {
		t.Errorf("sibling dir must not be shortened: %q", got)
	}
	if got := shortenHome("/etc"); got != "/etc" {
		t.Errorf("shortenHome(/etc) = %q", got)
	}
}
