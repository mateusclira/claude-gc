package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// launchd manages a per-user LaunchAgent on macOS.
type launchd struct {
	home string
	uid  int
}

func (l *launchd) UnitPath() string {
	return filepath.Join(l.home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

func (l *launchd) logFile() string {
	return filepath.Join(l.home, "Library", "Logs", "claude-gc", "claude-gc.log")
}

func (l *launchd) LogHint() string { return l.logFile() }

func (l *launchd) domain() string { return "gui/" + strconv.Itoa(l.uid) }
func (l *launchd) target() string { return l.domain() + "/" + LaunchdLabel }

func (l *launchd) render(cfg Config) ([]byte, error) {
	return render("launchd.plist.tmpl", map[string]any{
		"Label":   LaunchdLabel,
		"Argv":    cfg.Argv(),
		"LogFile": l.logFile(),
	})
}

func (l *launchd) Install(cfg Config) ([]string, error) {
	var notes []string
	if exists(filepath.Join(l.home, "Library", "LaunchAgents", brewLabel+".plist")) {
		notes = append(notes, "A Homebrew service for claude-gc is also installed; run `brew services stop claude-gc` so only one daemon runs.")
	}
	body, err := l.render(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(l.logFile()), 0o755); err != nil {
		return nil, err
	}
	if err := writeFile(l.UnitPath(), body); err != nil {
		return nil, err
	}
	// Unload any previous copy, make sure the label is not disabled from an
	// earlier uninstall, then load the fresh unit. RunAtLoad starts it.
	_, _ = run("launchctl", "bootout", l.target())
	_, _ = run("launchctl", "enable", l.target())
	if out, err := run("launchctl", "bootstrap", l.domain(), l.UnitPath()); err != nil {
		return notes, fmt.Errorf("launchctl bootstrap failed: %w\n%s", err, out)
	}
	return notes, nil
}

func (l *launchd) Uninstall() error {
	if out, err := run("launchctl", "bootout", l.target()); err != nil && !strings.Contains(out, "No such process") && exists(l.UnitPath()) {
		return fmt.Errorf("launchctl bootout failed: %w\n%s", err, out)
	}
	return removeFile(l.UnitPath())
}

func (l *launchd) Restart() error {
	if out, err := run("launchctl", "kickstart", "-k", l.target()); err != nil {
		return fmt.Errorf("launchctl kickstart failed: %w\n%s", err, out)
	}
	return nil
}

func (l *launchd) Status() (Status, error) {
	st := Status{UnitPath: l.UnitPath(), LogHint: l.LogHint(), Installed: exists(l.UnitPath())}
	out, err := run("launchctl", "print", l.target())
	if err != nil {
		return st, nil // not loaded
	}
	st.Installed = true
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "pid = "); ok {
			st.PID, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if v, ok := strings.CutPrefix(line, "state = "); ok {
			st.Running = strings.TrimSpace(v) == "running"
		}
	}
	if st.PID > 0 {
		st.Running = true
	}
	return st, nil
}

func (l *launchd) Logs(w io.Writer, follow bool) error {
	if !exists(l.logFile()) {
		return fmt.Errorf("no log file yet at %s", l.logFile())
	}
	args := []string{"-n", "50"}
	if follow {
		args = append(args, "-f")
	}
	return stream(w, "tail", append(args, l.logFile())...)
}
