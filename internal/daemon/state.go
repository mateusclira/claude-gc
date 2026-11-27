package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// retryAfterFailure is how long the daemon waits before retrying a pass that
// could not run, unless the interval is shorter.
const retryAfterFailure = time.Hour

// State is what the daemon records about itself. It keeps the schedule across
// restarts of the daemon, so logging out and back in does not cause an extra
// pass, and it lets `claude-gc service status` report the schedule. Fields
// are only ever added, so older state files keep loading.
type State struct {
	PID              int       `json:"pid"`
	ConfigDir        string    `json:"configDir"`
	Reaps            string    `json:"reaps,omitempty"`
	IntervalSeconds  int64     `json:"intervalSeconds"`
	MinUptimeSeconds int64     `json:"minUptimeSeconds"`
	Boot             time.Time `json:"boot,omitzero"`
	NextPass         time.Time `json:"nextPass,omitzero"`
	LastPass         *Pass     `json:"lastPass,omitempty"`
	LastError        *Failure  `json:"lastError,omitempty"`
}

// Pass summarises a pass that ran.
type Pass struct {
	At       time.Time `json:"at"`
	Sessions int       `json:"sessions"`
	Reaped   int       `json:"reaped"`
	FreedKB  int64     `json:"freedKB"`
	Forced   int       `json:"forced"`
	Removed  int       `json:"removedFiles"`
	Errors   int       `json:"errors"`
}

// Failure records a pass that could not run at all.
type Failure struct {
	At    time.Time `json:"at"`
	Error string    `json:"error"`
}

// Policy returns the schedule recorded in the state.
func (s State) Policy() Policy {
	return Policy{
		Interval:  time.Duration(s.IntervalSeconds) * time.Second,
		MinUptime: time.Duration(s.MinUptimeSeconds) * time.Second,
	}
}

// FailedSinceLastPass returns the most recent failure when it happened after
// the last pass that ran.
func (s State) FailedSinceLastPass() *Failure {
	if s.LastError == nil || (s.LastPass != nil && !s.LastError.At.After(s.LastPass.At)) {
		return nil
	}
	return s.LastError
}

// Next returns when the next pass is due: the policy applied to the last
// pass, held back for an hour after a pass that could not run.
func (s State) Next(now, boot time.Time) time.Time {
	p := s.Policy()
	var last time.Time
	if s.LastPass != nil {
		last = s.LastPass.At
	}
	next := p.Next(now, boot, last)
	if f := s.FailedSinceLastPass(); f != nil {
		at := f.At
		if at.After(now) {
			at = now
		}
		if retry := at.Add(min(retryAfterFailure, p.Interval)); retry.After(next) {
			next = retry
		}
	}
	return next
}

// StatePath returns where the daemon keeps its state:
// ~/Library/Application Support/claude-gc/state.json on macOS and
// ~/.local/state/claude-gc/state.json elsewhere. It depends only on the home
// directory, so the daemon and the command line always agree on it.
func StatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "claude-gc", "state.json"), nil
	}
	return filepath.Join(home, ".local", "state", "claude-gc", "state.json"), nil
}

// LoadState reads a state file. A missing file yields an empty state.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return s, nil
}

// SaveState writes a state file atomically.
func SaveState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
