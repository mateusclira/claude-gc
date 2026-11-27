// Package proc lists and signals processes through the portable ps(1)
// interface, so one code path serves macOS and Linux without cgo.
package proc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Process is one row of the process table.
type Process struct {
	PID   int       `json:"pid"`
	PPID  int       `json:"ppid"`
	RSSKB int64     `json:"rssKB"`           // resident set size in kibibytes
	Start time.Time `json:"start,omitempty"` // process start time (UTC, second resolution)
	Args  string    `json:"args"`            // full command line as reported by ps
}

// Name returns the executable's base name, derived from the command line.
func (p Process) Name() string {
	exe := p.Args
	if i := strings.IndexAny(exe, " \t"); i >= 0 {
		exe = exe[:i]
	}
	if exe == "" {
		return "?"
	}
	return filepath.Base(exe)
}

// Table maps PIDs to processes.
type Table map[int]Process

// execPS runs ps with a fixed locale and UTC so output is parseable and
// comparable with the start times Claude Code stores in session records.
var execPS = func(args ...string) ([]byte, error) {
	cmd := exec.Command("ps", args...)
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C", "LANG=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("ps %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Snapshot returns every process visible to the current user.
func Snapshot() (Table, error) {
	out, err := execPS("-e", "-ww", "-o", "pid=,ppid=,rss=,lstart=,args=")
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

// Parse parses the output of `ps -e -o pid=,ppid=,rss=,lstart=,args=`.
// Rows that do not parse are skipped.
func Parse(out []byte) (Table, error) {
	table := Table{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if p, ok := parseLine(sc.Text()); ok {
			table[p.PID] = p
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading ps output: %w", err)
	}
	return table, nil
}

func parseLine(line string) (Process, bool) {
	fields, rest := splitFields(line, 8)
	if len(fields) < 8 {
		return Process{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Process{}, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Process{}, false
	}
	rss, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return Process{}, false
	}
	// A start time that fails to parse leaves Start zero; the reaper then
	// refuses to touch the process because its identity cannot be verified.
	start, _ := ParseStart(strings.Join(fields[3:8], " "))
	return Process{PID: pid, PPID: ppid, RSSKB: rss, Start: start, Args: rest}, true
}

// splitFields returns the first n whitespace-separated fields of s and the
// remainder of the line with leading whitespace removed.
func splitFields(s string, n int) ([]string, string) {
	fields := make([]string, 0, n)
	i := 0
	for len(fields) < n {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		fields = append(fields, s[i:j])
		i = j
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return fields, s[i:]
}

// StartLayout is the Go layout of a ps(1) lstart value once its whitespace
// has been normalised, for example "Fri Nov 27 13:43:33 2026".
const StartLayout = "Mon Jan 2 15:04:05 2006"

// ParseStart parses a ps(1) lstart value as UTC. Claude Code stores the same
// string (captured with TZ=UTC) in its session records as "procStart".
func ParseStart(s string) (time.Time, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("unexpected process start time %q", s)
	}
	return time.ParseInLocation(StartLayout, strings.Join(fields, " "), time.UTC)
}

// StartTime asks ps for the start time of a single process. ok is false when
// the process no longer exists.
func StartTime(pid int) (start time.Time, ok bool, err error) {
	out, err := execPS("-o", "lstart=", "-p", strconv.Itoa(pid))
	s := strings.TrimSpace(string(out))
	if s == "" {
		// ps exits non-zero and prints nothing when the pid is gone.
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	start, err = ParseStart(s)
	if err != nil {
		return time.Time{}, true, err
	}
	return start, true, nil
}

// State returns the ps(1) state string of a process ("S", "Z", ...). ok is
// false when the process no longer exists.
func State(pid int) (state string, ok bool) {
	out, _ := execPS("-o", "stat=", "-p", strconv.Itoa(pid))
	s := strings.TrimSpace(string(out))
	return s, s != ""
}

// Ancestors returns pid and every ancestor of it found in the table.
func (t Table) Ancestors(pid int) map[int]bool {
	chain := map[int]bool{}
	for pid > 0 && !chain[pid] {
		chain[pid] = true
		p, ok := t[pid]
		if !ok {
			break
		}
		pid = p.PPID
	}
	return chain
}

// claudeRE matches a Claude Code process command line: the native binary
// ("…/claude", "claude --flags"), the native installer's versioned binary
// ("…/.local/share/claude/versions/2.1.215"), or the npm distribution run
// through node ("node …/@anthropic-ai/claude-code/cli.js").
var claudeRE = regexp.MustCompile(`(?:^|/)claude(?:\s|$)|/claude/versions/|claude-code/cli\.js`)

// IsClaude reports whether a command line belongs to Claude Code.
func IsClaude(args string) bool { return claudeRE.MatchString(args) }

// Alive reports whether a process exists. Processes owned by other users
// count as alive even though we could not signal them.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Gone reports whether a process has exited. A zombie counts as gone: it
// holds no memory and only waits for its parent to collect it.
func Gone(pid int) bool {
	if !Alive(pid) {
		return true
	}
	state, ok := State(pid)
	return !ok || strings.HasPrefix(state, "Z")
}

// Terminate asks a process to exit with SIGTERM, waits up to grace for it,
// then forces it with SIGKILL. forced reports whether SIGKILL was needed.
func Terminate(pid int, grace time.Duration) (forced bool, err error) {
	if pid <= 1 {
		return false, fmt.Errorf("refusing to signal pid %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}
	if waitGone(pid, grace) {
		return false, nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return true, fmt.Errorf("SIGKILL pid %d: %w", pid, err)
	}
	if !waitGone(pid, 3*time.Second) {
		return true, fmt.Errorf("pid %d is still running after SIGKILL", pid)
	}
	return true, nil
}

func waitGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if Gone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
