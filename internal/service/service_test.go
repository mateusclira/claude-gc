package service

import (
	"strings"
	"testing"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
)

var testConfig = Config{
	Binary:      "/opt/homebrew/bin/claude-gc",
	Interval:    30 * time.Minute,
	MaxIdle:     3 * humanize.Day,
	Entrypoints: []string{"claude-vscode"},
	Grace:       5 * time.Second,
}

func TestArgv(t *testing.T) {
	got := strings.Join(testConfig.Argv(), " ")
	want := "/opt/homebrew/bin/claude-gc daemon --interval 30m --max-idle 3d --grace 5s --entrypoints claude-vscode"
	if got != want {
		t.Errorf("Argv = %q\nwant  %q", got, want)
	}
	cfg := testConfig
	cfg.ConfigDir = "/home/me/claude"
	cfg.Verbose = true
	cfg.Entrypoints = []string{"claude-vscode", "cli"}
	got = strings.Join(cfg.Argv(), " ")
	if !strings.HasSuffix(got, "--entrypoints claude-vscode,cli --config-dir /home/me/claude --verbose") {
		t.Errorf("Argv = %q", got)
	}
}

func TestRenderLaunchd(t *testing.T) {
	t.Setenv("HOME", "/Users/me")
	cfg := testConfig
	cfg.Binary = "/Users/me/My Tools & Bins/claude-gc"
	body, err := Render("darwin", cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"<string>" + LaunchdLabel + "</string>",
		"<string>/Users/me/My Tools &amp; Bins/claude-gc</string>",
		"<string>daemon</string>",
		"<string>--max-idle</string>",
		"<string>3d</string>",
		"<key>KeepAlive</key>",
		"<string>/Users/me/Library/Logs/claude-gc/claude-gc.log</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q:\n%s", want, s)
		}
	}
	if !strings.HasPrefix(s, "<?xml") {
		t.Errorf("plist does not start with an XML declaration")
	}
}

func TestRenderSystemd(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("XDG_CONFIG_HOME", "")
	cfg := testConfig
	cfg.Binary = `/home/me/bin with "quotes"/claude-gc`
	body, err := Render("linux", cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	want := `ExecStart="/home/me/bin with \"quotes\"/claude-gc" "daemon" "--interval" "30m" "--max-idle" "3d" "--grace" "5s" "--entrypoints" "claude-vscode"`
	if !strings.Contains(s, want) {
		t.Errorf("unit missing %q:\n%s", want, s)
	}
	for _, want := range []string{"Restart=always", "WantedBy=default.target", "[Install]"} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	m, err := newFor("linux")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.UnitPath(); got != "/home/me/.config/systemd/user/claude-gc.service" {
		t.Errorf("UnitPath = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	m, _ = newFor("linux")
	if got := m.UnitPath(); got != "/xdg/systemd/user/claude-gc.service" {
		t.Errorf("UnitPath with XDG_CONFIG_HOME = %q", got)
	}
}

func TestUnsupported(t *testing.T) {
	if _, err := newFor("windows"); err == nil {
		t.Error("expected error for windows")
	}
}
