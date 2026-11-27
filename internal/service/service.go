// Package service installs claude-gc as a per-user background service: a
// launchd agent on macOS or a systemd user unit on Linux.
package service

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
)

//go:embed templates/*.tmpl
var templates embed.FS

// Identifiers of the service in each service manager.
const (
	LaunchdLabel = "io.github.mateusclira.claude-gc"
	SystemdUnit  = "claude-gc.service"
	brewLabel    = "homebrew.mxcl.claude-gc"
)

// Config describes the daemon command the service runs.
type Config struct {
	Binary      string
	ConfigDir   string        // passed as -config-dir when set
	Interval    time.Duration // -fq
	MinUptime   time.Duration // -min-uptime
	MaxIdle     time.Duration // -life
	Entrypoints []string
	Grace       time.Duration
	Verbose     bool
}

// Argv returns the full command line the service runs.
func (c Config) Argv() []string {
	argv := []string{c.Binary, "daemon",
		"-fq", humanize.FormatDuration(c.Interval),
		"-life", humanize.FormatDuration(c.MaxIdle),
		"-min-uptime", humanize.FormatDuration(c.MinUptime),
		"-grace", humanize.FormatDuration(c.Grace),
	}
	if len(c.Entrypoints) > 0 {
		argv = append(argv, "-entrypoints", strings.Join(c.Entrypoints, ","))
	}
	if c.ConfigDir != "" {
		argv = append(argv, "-config-dir", c.ConfigDir)
	}
	if c.Verbose {
		argv = append(argv, "-verbose")
	}
	return argv
}

// Status describes the installed service.
type Status struct {
	Installed bool
	Running   bool
	PID       int
	UnitPath  string
	LogHint   string
}

// Manager installs and inspects the service on one platform.
type Manager interface {
	// Install writes the unit, (re)loads it and starts the daemon. The
	// returned notes are advice worth showing the user.
	Install(cfg Config) (notes []string, err error)
	// Uninstall stops the daemon and removes the unit.
	Uninstall() error
	// Status reports whether the service is installed and running.
	Status() (Status, error)
	// Restart restarts the daemon from its current unit, so it picks up a
	// new binary at the same path and keeps its flags.
	Restart() error
	// Logs writes recent daemon logs to w, following them when follow is set.
	Logs(w io.Writer, follow bool) error
	// UnitPath is where the unit file lives.
	UnitPath() string
	// LogHint says where logs can be found.
	LogHint() string
}

// New returns the service manager for the current platform.
func New() (Manager, error) {
	return newFor(runtime.GOOS)
}

func newFor(goos string) (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch goos {
	case "darwin":
		return &launchd{home: home, uid: os.Getuid()}, nil
	case "linux":
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		return &systemd{home: home, configHome: configHome}, nil
	default:
		return nil, fmt.Errorf("background service is not supported on %s; run `claude-gc daemon` under a supervisor of your choice", goos)
	}
}

// Render produces the unit file for the given platform. It is exported so
// the unit can be inspected without installing it.
func Render(goos string, cfg Config) ([]byte, error) {
	m, err := newFor(goos)
	if err != nil {
		return nil, err
	}
	switch m := m.(type) {
	case *launchd:
		return m.render(cfg)
	case *systemd:
		return m.render(cfg)
	}
	return nil, errors.New("unreachable")
}

func render(name string, data any) ([]byte, error) {
	t, err := template.New(name).Funcs(template.FuncMap{
		"xml":   xmlEscape,
		"quote": systemdQuote,
	}).ParseFS(templates, "templates/"+name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Runs reports whether the installed unit runs binary, written exactly as
// Install writes it.
func Runs(m Manager, binary string) bool {
	unit, err := os.ReadFile(m.UnitPath())
	if err != nil || binary == "" {
		return false
	}
	return bytes.Contains(unit, []byte("<string>"+xmlEscape(binary)+"</string>")) ||
		bytes.Contains(unit, []byte(systemdQuote(binary)))
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// systemdQuote quotes one ExecStart argument.
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// run executes a command and returns its combined output.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// stream runs a command with its output attached to w.
func stream(w io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}
