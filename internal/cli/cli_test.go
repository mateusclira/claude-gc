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
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/service"
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
		{"zero life", []string{"status", "-life", "0"}, nil, "-life must be greater than zero"},
		{"zero life, old name", []string{"status", "--max-idle", "0"}, nil, "-life must be greater than zero"},
		{"unitless life", []string{"status", "-life", "3"}, nil, "missing unit"},
		{"bad life", []string{"status", "-life", "soon"}, nil, `invalid value "soon"`},
		{"bad life env", []string{"status"}, map[string]string{"CLAUDE_GC_LIFE": "never"}, "CLAUDE_GC_LIFE"},
		{"bad old life env", []string{"status"}, map[string]string{"CLAUDE_GC_MAX_IDLE": "never"}, "CLAUDE_GC_MAX_IDLE"},
		{"short fq", []string{"daemon", "-fq", "5s"}, nil, "-fq must be at least 1m"},
		{"short fq, old name", []string{"daemon", "--interval", "5s"}, nil, "-fq must be at least 1m"},
		{"bad fq env", []string{"daemon"}, map[string]string{"CLAUDE_GC_FQ": "often"}, "CLAUDE_GC_FQ"},
		{"fq on status", []string{"status", "-fq", "1h"}, nil, "flag provided but not defined: -fq"},
		{"old fq name on status", []string{"status", "-interval", "1h"}, nil, "flag provided but not defined: -interval"},
		{"bad min-uptime", []string{"daemon", "-min-uptime", "-1h"}, nil, `invalid value "-1h"`},
		{"bad min-uptime env", []string{"daemon"}, map[string]string{"CLAUDE_GC_MIN_UPTIME": "a while"}, "CLAUDE_GC_MIN_UPTIME"},
		{"empty entrypoints", []string{"status", "-entrypoints", " , "}, nil, "-entrypoints must name"},
		{"positional", []string{"status", "extra"}, nil, `unexpected argument "extra"`},
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

// parseSchedule parses args the way the daemon and service install do.
func parseSchedule(t *testing.T, args ...string) *scanFlags {
	t.Helper()
	a, _, _ := newTestApp()
	var sf scanFlags
	fs := a.flagSet("test", &sf, "")
	sf.addScheduleFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	if err := sf.resolve(fs); err != nil {
		t.Fatalf("resolve %v: %v", args, err)
	}
	return &sf
}

func TestFqAndLife(t *testing.T) {
	sf := parseSchedule(t, "-fq", "30m", "-life", "2h")
	if sf.fq.d != 30*time.Minute || sf.life.d != 2*time.Hour {
		t.Errorf("fq = %v, life = %v", sf.fq.d, sf.life.d)
	}
	if got := sf.policy().Describe(); got != "every 30m, after 2 hours of uptime" {
		t.Errorf("policy = %q", got)
	}
	if got := sf.reaps(); got != "claude-vscode sessions idle for longer than 2h" {
		t.Errorf("reaps = %q", got)
	}
	// Every spelling the flag package allows.
	for _, args := range [][]string{
		{"--fq", "2h", "--life", "3d"},
		{"-fq=2h", "-life=3d"},
		{"--fq=2h", "--life=3d"},
	} {
		sf := parseSchedule(t, args...)
		if sf.fq.d != 2*time.Hour || sf.life.d != 3*humanize.Day {
			t.Errorf("%v: fq = %v, life = %v", args, sf.fq.d, sf.life.d)
		}
	}
}

func TestOldFlagNamesStillWork(t *testing.T) {
	// The exact arguments a service installed by v0.2.0 starts the daemon with.
	sf := parseSchedule(t, "--interval", "12h", "--min-uptime", "24h", "--max-idle", "2d", "--grace", "5s", "--entrypoints", "claude-vscode")
	if sf.fq.d != 12*time.Hour || sf.life.d != 2*humanize.Day || sf.minUptime.d != 24*time.Hour {
		t.Errorf("fq = %v, life = %v, min-uptime = %v", sf.fq.d, sf.life.d, sf.minUptime.d)
	}
}

func TestMinUptimeFollowsShortLife(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  string
		want time.Duration
	}{
		{"default life", nil, "", 24 * time.Hour},
		{"long life", []string{"-life", "7d"}, "", 24 * time.Hour},
		{"short life", []string{"-life", "2h"}, "", 2 * time.Hour},
		{"short life, explicit wait", []string{"-life", "2h", "-min-uptime", "24h"}, "", 24 * time.Hour},
		{"short life, rule off", []string{"-life", "2h", "-min-uptime", "0"}, "", 0},
		{"short life, wait from env", []string{"-life", "2h"}, "6h", 6 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_GC_MIN_UPTIME", c.env)
			if sf := parseSchedule(t, c.args...); sf.minUptime.d != c.want {
				t.Errorf("min-uptime = %v, want %v", sf.minUptime.d, c.want)
			}
		})
	}
}

func TestEnvDefaults(t *testing.T) {
	t.Setenv("CLAUDE_GC_LIFE", "1d")
	t.Setenv("CLAUDE_GC_FQ", "2h")
	t.Setenv("CLAUDE_GC_ENTRYPOINTS", "claude-vscode, cli")
	t.Setenv("CLAUDE_GC_GRACE", "9s")
	t.Setenv("CLAUDE_GC_MIN_UPTIME", "0")
	sf := parseSchedule(t, "-grace", "1s")
	if sf.life.d != 24*time.Hour {
		t.Errorf("life from env = %v, want 24h", sf.life.d)
	}
	if sf.fq.d != 2*time.Hour {
		t.Errorf("fq from env = %v, want 2h", sf.fq.d)
	}
	if got := strings.Join(sf.entrypointList(), "|"); got != "claude-vscode|cli" {
		t.Errorf("entrypoints from env = %q", got)
	}
	if sf.grace.d != time.Second {
		t.Errorf("an explicit flag must win over the environment: grace = %v", sf.grace.d)
	}
	if sf.minUptime.d != 0 {
		t.Errorf("min-uptime from env = %v, want 0 (disabled)", sf.minUptime.d)
	}
	if got := sf.policy().Describe(); got != "every 2 hours" {
		t.Errorf("policy = %q", got)
	}
}

func TestOldEnvNames(t *testing.T) {
	t.Setenv("CLAUDE_GC_MAX_IDLE", "1d")
	t.Setenv("CLAUDE_GC_INTERVAL", "2h")
	sf := parseSchedule(t)
	if sf.life.d != 24*time.Hour || sf.fq.d != 2*time.Hour {
		t.Errorf("old names: life = %v, fq = %v", sf.life.d, sf.fq.d)
	}
	t.Setenv("CLAUDE_GC_LIFE", "5d")
	t.Setenv("CLAUDE_GC_FQ", "6h")
	sf = parseSchedule(t)
	if sf.life.d != 5*humanize.Day || sf.fq.d != 6*time.Hour {
		t.Errorf("new names must win over old ones: life = %v, fq = %v", sf.life.d, sf.fq.d)
	}
}

func TestServiceArgvRoundTrip(t *testing.T) {
	// Whatever service install writes into the unit file, the daemon must
	// read back as the same settings.
	want := parseSchedule(t, "-fq", "2h", "-life", "36h", "-grace", "9s", "-entrypoints", "claude-vscode,cli", "-config-dir", "/srv/claude", "-verbose")
	cfg := service.Config{
		Binary:      "/usr/local/bin/claude-gc",
		ConfigDir:   want.configDir,
		Interval:    want.fq.d,
		MinUptime:   want.minUptime.d,
		MaxIdle:     want.life.d,
		Entrypoints: want.entrypointList(),
		Grace:       want.grace.d,
		Verbose:     want.verbose,
	}
	argv := cfg.Argv()
	if argv[1] != "daemon" {
		t.Fatalf("argv = %v", argv)
	}
	got := parseSchedule(t, argv[2:]...)
	if got.fq != want.fq || got.life != want.life || got.minUptime != want.minUptime || got.grace != want.grace ||
		got.entrypoints != want.entrypoints || got.configDir != want.configDir || got.verbose != want.verbose {
		t.Errorf("round trip through %v:\n got %+v\nwant %+v", argv, *got, *want)
	}
}

func TestHelpShowsNewNames(t *testing.T) {
	for _, cmd := range []string{"daemon", "status"} {
		a, _, errb := newTestApp()
		if code := a.run([]string{cmd, "-h"}); code != 0 {
			t.Fatalf("%s -h: code %d", cmd, code)
		}
		help := errb.String()
		for _, want := range []string{"-life duration", "Flags take one dash or two"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s -h missing %q:\n%s", cmd, want, help)
			}
		}
		for _, old := range []string{"-max-idle", "-interval"} {
			if strings.Contains(help, old) {
				t.Errorf("%s -h shows the old name %q:\n%s", cmd, old, help)
			}
		}
		if hasFq := strings.Contains(help, "-fq duration"); hasFq != (cmd == "daemon") {
			t.Errorf("%s -h: shows -fq = %v", cmd, hasFq)
		}
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
	t.Setenv("CLAUDE_GC_FQ", "5s")
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
