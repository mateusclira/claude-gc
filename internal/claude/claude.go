// Package claude knows where Claude Code keeps its on-disk state and how to
// read it: session records, transcripts and messaging sockets.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mateusclira/claude-gc/internal/proc"
)

// ConfigDir returns Claude Code's configuration directory: $CLAUDE_CONFIG_DIR
// when set, otherwise ~/.claude.
func ConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// SessionsDir holds one JSON record per running session, named "<pid>.json",
// plus peer key files named "<pid>.<hash>.key".
func SessionsDir(configDir string) string { return filepath.Join(configDir, "sessions") }

// ProjectsDir holds one directory per project, each containing one
// "<sessionId>.jsonl" transcript per session that ran there.
func ProjectsDir(configDir string) string { return filepath.Join(configDir, "projects") }

// SocketDirs returns the existing directories in which Claude Code creates
// per-session messaging sockets named "<pid>.sock".
func SocketDirs() []string {
	candidates := []string{"/tmp/cc-socks"}
	if t := os.Getenv("TMPDIR"); t != "" {
		candidates = append(candidates, filepath.Join(t, "cc-socks"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "cc-socks"))

	seen := map[string]bool{}
	var dirs []string
	for _, d := range candidates {
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// Record is the session file Claude Code writes to <sessions>/<pid>.json.
// Only the fields claude-gc uses are listed; unknown fields are ignored.
type Record struct {
	PID        int    `json:"pid"`
	Path       string `json:"-"`
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"` // milliseconds since the Unix epoch
	ProcStart  string `json:"procStart"` // `TZ=UTC ps -o lstart=` of the process
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	SocketPath string `json:"messagingSocketPath"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// Started returns when the session started, or the zero time when unknown.
func (r Record) Started() time.Time {
	if r.StartedAt <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(r.StartedAt).UTC()
}

// ProcStartTime parses the recorded process start time.
func (r Record) ProcStartTime() (time.Time, bool) {
	if r.ProcStart == "" {
		return time.Time{}, false
	}
	t, err := proc.ParseStart(r.ProcStart)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// DisplayName returns the session's name, falling back to its id.
func (r Record) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	if len(r.SessionID) >= 8 {
		return r.SessionID[:8]
	}
	return "?"
}

// ReadRecords reads every session record in dir, sorted by PID. A missing
// directory yields no records and no problems. Files that cannot be read or
// parsed are reported in problems and skipped.
func ReadRecords(dir string) (records []Record, problems []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("reading %s: %w", dir, err)}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Errorf("reading %s: %w", path, err))
			continue
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			problems = append(problems, fmt.Errorf("parsing %s: %w", path, err))
			continue
		}
		r.PID = pid // the file name is authoritative
		r.Path = path
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].PID < records[j].PID })
	return records, problems
}

// RelatedFiles lists the files in dir that belong to a session: its record
// ("<pid>.json") and its peer key files ("<pid>.<hash>.key").
func RelatedFiles(dir string, pid int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := strconv.Itoa(pid) + "."
	var files []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) {
			continue
		}
		if strings.HasSuffix(n, ".json") || strings.HasSuffix(n, ".key") {
			files = append(files, filepath.Join(dir, n))
		}
	}
	sort.Strings(files)
	return files
}

// Transcripts indexes the transcripts under projectsDir, mapping each session
// id to its transcript's last modification time. A missing directory yields
// an empty index.
func Transcripts(projectsDir string) (map[string]time.Time, error) {
	index := map[string]time.Time{}
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return index, nil
		}
		return nil, fmt.Errorf("reading %s: %w", projectsDir, err)
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(projectsDir, p.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			if prev, ok := index[id]; !ok || info.ModTime().After(prev) {
				index[id] = info.ModTime()
			}
		}
	}
	return index, nil
}

// SocketPID extracts the PID from a socket file name such as "95113.sock".
func SocketPID(name string) (int, bool) {
	if !strings.HasSuffix(name, ".sock") {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(name, ".sock"))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// IsSocket reports whether path exists and is a Unix socket.
func IsSocket(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
