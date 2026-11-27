// Package reaper decides which Claude Code sessions are abandoned and reaps
// them. Scan is read-only and produces a Report; Apply acts on it.
package reaper

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mateusclira/claude-gc/internal/claude"
	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/proc"
)

// Action is what claude-gc will do with a session.
type Action string

// The possible actions, in the order they are reported.
const (
	Reap  Action = "reap"  // idle for longer than allowed: terminate, then clean up
	Keep  Action = "keep"  // alive and recently active
	Skip  Action = "skip"  // alive but out of scope (own session, entrypoint, unverifiable)
	Clean Action = "clean" // process already gone: remove leftover files only
)

// DefaultEntrypoints lists the session entrypoints that are reaped unless
// configured otherwise. "claude-vscode" sessions are the ones the VS Code
// extension spawns and forgets.
var DefaultEntrypoints = []string{"claude-vscode"}

// identityTolerance is how far a process start time may drift from the
// recorded one and still be considered the same process.
const identityTolerance = 5 * time.Second

// Session is a Claude Code session record joined with its live process.
type Session struct {
	Record       claude.Record `json:"record"`
	Process      *proc.Process `json:"process,omitempty"`
	Verified     bool          `json:"verified"`
	LastActivity time.Time     `json:"lastActivity"` // zero when no transcript exists
	Idle         time.Duration `json:"idle"`
	Action       Action        `json:"action"`
	Reason       string        `json:"reason"`
	Files        []string      `json:"files,omitempty"` // leftovers removed on reap or clean
}

// RSSKB returns the session's resident memory in kibibytes, or 0 when the
// process is gone.
func (s Session) RSSKB() int64 {
	if s.Process == nil {
		return 0
	}
	return s.Process.RSSKB
}

// Options configure a scan. Zero values select sensible defaults; the
// function fields exist so tests can substitute the process table.
type Options struct {
	ConfigDir   string
	MaxIdle     time.Duration
	Entrypoints []string // nil = DefaultEntrypoints; "*" matches every entrypoint
	Now         time.Time
	SelfPID     int

	Snapshot    func() (proc.Table, error)
	IsClaude    func(args string) bool
	Transcripts func(projectsDir string) (map[string]time.Time, error)
	SocketDirs  func() []string
}

func (o Options) withDefaults() Options {
	if o.ConfigDir == "" {
		o.ConfigDir = claude.ConfigDir()
	}
	if o.MaxIdle <= 0 {
		o.MaxIdle = 3 * humanize.Day
	}
	if len(o.Entrypoints) == 0 {
		o.Entrypoints = DefaultEntrypoints
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.SelfPID == 0 {
		o.SelfPID = os.Getpid()
	}
	if o.Snapshot == nil {
		o.Snapshot = proc.Snapshot
	}
	if o.IsClaude == nil {
		o.IsClaude = proc.IsClaude
	}
	if o.Transcripts == nil {
		o.Transcripts = claude.Transcripts
	}
	if o.SocketDirs == nil {
		o.SocketDirs = claude.SocketDirs
	}
	return o
}

// Report is the outcome of a scan.
type Report struct {
	Now          time.Time      `json:"now"`
	ConfigDir    string         `json:"configDir"`
	MaxIdle      time.Duration  `json:"maxIdle"`
	Entrypoints  []string       `json:"entrypoints"`
	Sessions     []Session      `json:"sessions"`
	Unknown      []proc.Process `json:"unknown"`      // claude processes without a session record
	StaleSockets []string       `json:"staleSockets"` // sockets whose owning process is gone
	Problems     []error        `json:"-"`
}

// Count returns how many sessions have the given action.
func (r *Report) Count(a Action) int {
	n := 0
	for _, s := range r.Sessions {
		if s.Action == a {
			n++
		}
	}
	return n
}

// ByAction returns the sessions with the given action, in report order.
func (r *Report) ByAction(a Action) []Session {
	var out []Session
	for _, s := range r.Sessions {
		if s.Action == a {
			out = append(out, s)
		}
	}
	return out
}

// ReapableKB sums the resident memory of every session due to be reaped.
func (r *Report) ReapableKB() int64 {
	var total int64
	for _, s := range r.Sessions {
		if s.Action == Reap {
			total += s.RSSKB()
		}
	}
	return total
}

// ResidentKB sums the resident memory of every live session.
func (r *Report) ResidentKB() int64 {
	var total int64
	for _, s := range r.Sessions {
		total += s.RSSKB()
	}
	return total
}

// Scan joins Claude Code's session records with the process table and decides
// what to do with each session. It never modifies anything.
func Scan(o Options) (*Report, error) {
	o = o.withDefaults()
	table, err := o.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}
	transcripts, err := o.Transcripts(claude.ProjectsDir(o.ConfigDir))
	if err != nil {
		// Without the activity signal every session would look abandoned,
		// so refuse to decide anything.
		return nil, fmt.Errorf("indexing transcripts: %w", err)
	}
	sessionsDir := claude.SessionsDir(o.ConfigDir)
	records, problems := claude.ReadRecords(sessionsDir)

	rep := &Report{Now: o.Now, ConfigDir: o.ConfigDir, MaxIdle: o.MaxIdle, Entrypoints: o.Entrypoints, Problems: problems}
	protected := table.Ancestors(o.SelfPID)
	known := map[int]bool{}
	claimed := map[string]bool{}
	for _, rec := range records {
		known[rec.PID] = true
		s := decide(o, rec, table, protected, transcripts)
		if s.Action == Reap || s.Action == Clean {
			s.Files = leftovers(sessionsDir, rec)
			for _, f := range s.Files {
				claimed[f] = true
			}
		}
		rep.Sessions = append(rep.Sessions, s)
	}
	sort.SliceStable(rep.Sessions, func(i, j int) bool {
		a, b := rep.Sessions[i], rep.Sessions[j]
		if a.Idle != b.Idle {
			return a.Idle > b.Idle
		}
		return a.Record.PID < b.Record.PID
	})

	for pid, p := range table {
		if !known[pid] && o.IsClaude(p.Args) {
			rep.Unknown = append(rep.Unknown, p)
		}
	}
	sort.Slice(rep.Unknown, func(i, j int) bool { return rep.Unknown[i].PID < rep.Unknown[j].PID })

	for _, dir := range o.SocketDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			pid, ok := claude.SocketPID(e.Name())
			if !ok || e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if claimed[path] {
				continue
			}
			if p, alive := table[pid]; !alive || !o.IsClaude(p.Args) {
				rep.StaleSockets = append(rep.StaleSockets, path)
			}
		}
	}
	sort.Strings(rep.StaleSockets)
	return rep, nil
}

func decide(o Options, rec claude.Record, table proc.Table, protected map[int]bool, transcripts map[string]time.Time) Session {
	s := Session{Record: rec}
	p, alive := table[rec.PID]
	if !alive {
		s.Action, s.Reason = Clean, "process exited"
		s.Idle = idleSince(o.Now, rec.Started())
		return s
	}
	if !o.IsClaude(p.Args) {
		// The pid now belongs to something else; the session is gone.
		s.Action, s.Reason = Clean, "pid reused by "+p.Name()
		s.Idle = idleSince(o.Now, rec.Started())
		return s
	}
	s.Process = &p

	if last, ok := transcripts[rec.SessionID]; ok {
		s.LastActivity = last
		s.Idle = o.Now.Sub(last)
	} else {
		started := rec.Started()
		if started.IsZero() {
			started = p.Start
		}
		s.Idle = idleSince(o.Now, started)
	}
	if s.Idle < 0 {
		s.Idle = 0
	}

	switch verify(rec, p) {
	case verified:
		s.Verified = true
	case mismatch:
		s.Action, s.Reason = Skip, "start time does not match record"
		return s
	default:
		s.Action, s.Reason = Skip, "cannot verify process identity"
		return s
	}
	if protected[rec.PID] {
		s.Action, s.Reason = Skip, "current session"
		return s
	}
	if !entrypointAllowed(o.Entrypoints, rec.Entrypoint) {
		s.Action, s.Reason = Skip, "entrypoint "+orUnknown(rec.Entrypoint)
		return s
	}

	if s.LastActivity.IsZero() {
		s.Reason = fmt.Sprintf("no transcript, started %s ago", humanize.Duration(s.Idle))
	} else {
		s.Reason = "idle " + humanize.Duration(s.Idle)
	}
	if s.Idle > o.MaxIdle {
		s.Action = Reap
	} else {
		s.Action = Keep
	}
	return s
}

type verdict int

const (
	unverifiable verdict = iota
	verified
	mismatch
)

// verify checks that the live process is the one the record describes, so a
// recycled pid is never mistaken for an old session. Claude Code stores the
// process start time captured with TZ=UTC; a build that stored local time is
// tolerated by also comparing against the local interpretation.
func verify(rec claude.Record, p proc.Process) verdict {
	if p.Start.IsZero() {
		return unverifiable
	}
	if rec.ProcStart != "" {
		recorded, ok := rec.ProcStartTime()
		if !ok {
			return unverifiable
		}
		if absDiff(recorded, p.Start) <= identityTolerance {
			return verified
		}
		if local, err := time.ParseInLocation(proc.StartLayout, recorded.Format(proc.StartLayout), time.Local); err == nil &&
			absDiff(local, p.Start) <= identityTolerance {
			return verified
		}
		return mismatch
	}
	// Older records only carry the session start, written moments after
	// the process started.
	if started := rec.Started(); !started.IsZero() {
		if absDiff(started, p.Start) <= 5*time.Minute {
			return verified
		}
		return mismatch
	}
	return unverifiable
}

func absDiff(a, b time.Time) time.Duration {
	d := a.Sub(b)
	if d < 0 {
		return -d
	}
	return d
}

func idleSince(now, t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return now.Sub(t)
}

func entrypointAllowed(allowed []string, entrypoint string) bool {
	for _, a := range allowed {
		if a == "*" || a == entrypoint {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// leftovers lists the files a session leaves behind: its record, key files
// and messaging socket.
func leftovers(sessionsDir string, rec claude.Record) []string {
	files := claude.RelatedFiles(sessionsDir, rec.PID)
	if rec.SocketPath != "" && claude.IsSocket(rec.SocketPath) {
		files = append(files, rec.SocketPath)
	}
	return files
}

// ApplyOptions configure Apply. The function fields exist so tests can
// substitute the process operations.
type ApplyOptions struct {
	DryRun bool
	Grace  time.Duration
	Log    *slog.Logger

	Terminate func(pid int, grace time.Duration) (forced bool, err error)
	StartTime func(pid int) (start time.Time, ok bool, err error)
	Remove    func(path string) error
}

func (o ApplyOptions) withDefaults() ApplyOptions {
	if o.Grace <= 0 {
		o.Grace = 5 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Terminate == nil {
		o.Terminate = proc.Terminate
	}
	if o.StartTime == nil {
		o.StartTime = proc.StartTime
	}
	if o.Remove == nil {
		o.Remove = os.Remove
	}
	return o
}

// Result is what Apply did, or would have done in a dry run.
type Result struct {
	DryRun  bool      `json:"dryRun"`
	Reaped  []Session `json:"reaped"`
	Cleaned []Session `json:"cleaned"` // records whose process was already gone
	Removed []string  `json:"removed"` // leftover files removed
	Forced  int       `json:"forced"`  // sessions that needed SIGKILL
	FreedKB int64     `json:"freedKB"`
	Errors  []error   `json:"-"`
}

// Apply carries out a report: terminates the sessions marked Reap and removes
// the leftovers of those and of sessions marked Clean, plus stale sockets.
// With DryRun set it only records what it would do.
func Apply(rep *Report, o ApplyOptions) *Result {
	o = o.withDefaults()
	res := &Result{DryRun: o.DryRun}
	removeAll := func(files []string) {
		for _, f := range files {
			if o.DryRun {
				res.Removed = append(res.Removed, f)
				continue
			}
			if err := o.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				res.Errors = append(res.Errors, fmt.Errorf("removing %s: %w", f, err))
				continue
			}
			res.Removed = append(res.Removed, f)
		}
	}

	for _, s := range rep.Sessions {
		switch s.Action {
		case Reap:
			pid := s.Record.PID
			if o.DryRun {
				res.Reaped = append(res.Reaped, s)
				res.FreedKB += s.RSSKB()
				removeAll(s.Files)
				continue
			}
			// Re-check identity right before signalling: the pid could
			// have been recycled since the scan.
			start, ok, err := o.StartTime(pid)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("pid %d: %w", pid, err))
				continue
			}
			if !ok {
				o.Log.Info("session exited before it could be reaped", "pid", pid, "name", s.Record.DisplayName())
				res.Cleaned = append(res.Cleaned, s)
				removeAll(s.Files)
				continue
			}
			if s.Process != nil && !s.Process.Start.IsZero() && absDiff(start, s.Process.Start) > identityTolerance {
				res.Errors = append(res.Errors, fmt.Errorf("pid %d changed identity since the scan; left alone", pid))
				continue
			}
			forced, err := o.Terminate(pid, o.Grace)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("reaping pid %d (%s): %w", pid, s.Record.DisplayName(), err))
				continue
			}
			if forced {
				res.Forced++
			}
			res.Reaped = append(res.Reaped, s)
			res.FreedKB += s.RSSKB()
			o.Log.Info("reaped session",
				"pid", pid,
				"name", s.Record.DisplayName(),
				"project", s.Record.CWD,
				"rss", humanize.KB(s.RSSKB()),
				"reason", s.Reason,
				"forced", forced)
			removeAll(s.Files)
		case Clean:
			res.Cleaned = append(res.Cleaned, s)
			removeAll(s.Files)
		}
	}
	removeAll(rep.StaleSockets)
	return res
}
