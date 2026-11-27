package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"zero max-idle", []string{"status", "--max-idle", "0s"}, nil, "--max-idle must be greater than zero"},
		{"unitless max-idle", []string{"status", "--max-idle", "0"}, nil, "missing unit"},
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
	a, _, _ := newTestApp()
	var sf scanFlags
	fs := a.flagSet("test", &sf, "")
	if err := fs.Parse([]string{"--grace", "1s"}); err != nil {
		t.Fatal(err)
	}
	if err := sf.resolve(fs); err != nil {
		t.Fatal(err)
	}
	if got := sf.maxIdle.String(); got != "1d" {
		t.Errorf("max-idle from env = %q", got)
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
