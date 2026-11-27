package service

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// systemd manages a systemd user unit on Linux.
type systemd struct {
	home       string
	configHome string
}

func (s *systemd) UnitPath() string {
	return filepath.Join(s.configHome, "systemd", "user", SystemdUnit)
}

func (s *systemd) LogHint() string { return "journalctl --user -u claude-gc -f" }

func (s *systemd) render(cfg Config) ([]byte, error) {
	return render("systemd.service.tmpl", map[string]any{"Argv": cfg.Argv()})
}

func (s *systemd) Install(cfg Config) ([]string, error) {
	body, err := s.render(cfg)
	if err != nil {
		return nil, err
	}
	if err := writeFile(s.UnitPath(), body); err != nil {
		return nil, err
	}
	if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return nil, fmt.Errorf("systemctl --user daemon-reload failed: %w\n%s\n(is a systemd user session running? WSL and containers often lack one)", err, out)
	}
	if out, err := run("systemctl", "--user", "enable", SystemdUnit); err != nil {
		return nil, fmt.Errorf("systemctl --user enable failed: %w\n%s", err, out)
	}
	if out, err := run("systemctl", "--user", "restart", SystemdUnit); err != nil {
		return nil, fmt.Errorf("systemctl --user restart failed: %w\n%s", err, out)
	}
	var notes []string
	if !lingerEnabled() {
		name := os.Getenv("USER")
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		notes = append(notes, fmt.Sprintf("User services stop when you log out. Run `loginctl enable-linger %s` to keep claude-gc running.", name))
	}
	return notes, nil
}

func lingerEnabled() bool {
	out, err := run("loginctl", "show-user", strconv.Itoa(os.Getuid()), "--property=Linger")
	return err == nil && strings.Contains(out, "Linger=yes")
}

func (s *systemd) Uninstall() error {
	if exists(s.UnitPath()) {
		if out, err := run("systemctl", "--user", "disable", "--now", SystemdUnit); err != nil {
			return fmt.Errorf("systemctl --user disable --now failed: %w\n%s", err, out)
		}
	}
	if err := removeFile(s.UnitPath()); err != nil {
		return err
	}
	_, _ = run("systemctl", "--user", "daemon-reload")
	return nil
}

func (s *systemd) Restart() error {
	if out, err := run("systemctl", "--user", "restart", SystemdUnit); err != nil {
		return fmt.Errorf("systemctl --user restart failed: %w\n%s", err, out)
	}
	return nil
}

func (s *systemd) Status() (Status, error) {
	st := Status{UnitPath: s.UnitPath(), LogHint: s.LogHint(), Installed: exists(s.UnitPath())}
	out, err := run("systemctl", "--user", "show", SystemdUnit, "--property=ActiveState", "--property=MainPID")
	if err != nil {
		return st, nil
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "ActiveState="); ok {
			st.Running = strings.TrimSpace(v) == "active"
		}
		if v, ok := strings.CutPrefix(line, "MainPID="); ok {
			st.PID, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return st, nil
}

func (s *systemd) Logs(w io.Writer, follow bool) error {
	args := []string{"--user", "-u", SystemdUnit, "-n", "50", "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return stream(w, "journalctl", args...)
}
