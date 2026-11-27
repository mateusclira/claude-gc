package cli

import (
	"time"

	"github.com/mateusclira/claude-gc/internal/proc"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

// The JSON shapes printed by --json. They are part of the public interface,
// so fields are only ever added.

type sessionJSON struct {
	PID          int        `json:"pid"`
	Name         string     `json:"name"`
	SessionID    string     `json:"sessionId"`
	Project      string     `json:"project"`
	Entrypoint   string     `json:"entrypoint"`
	Version      string     `json:"version,omitempty"`
	RSSKB        int64      `json:"rssKB"`
	Started      *time.Time `json:"started,omitempty"`
	LastActivity *time.Time `json:"lastActivity,omitempty"`
	IdleSeconds  int64      `json:"idleSeconds"`
	Verified     bool       `json:"verified"`
	Action       string     `json:"action"`
	Reason       string     `json:"reason"`
	Files        []string   `json:"files,omitempty"`
}

type summaryJSON struct {
	Sessions   int   `json:"sessions"`
	Reap       int   `json:"reap"`
	Keep       int   `json:"keep"`
	Skip       int   `json:"skip"`
	Clean      int   `json:"clean"`
	ResidentKB int64 `json:"residentKB"`
	ReapableKB int64 `json:"reapableKB"`
}

type reportJSON struct {
	Now            time.Time      `json:"now"`
	ConfigDir      string         `json:"configDir"`
	MaxIdleSeconds int64          `json:"maxIdleSeconds"`
	Entrypoints    []string       `json:"entrypoints"`
	Summary        summaryJSON    `json:"summary"`
	Sessions       []sessionJSON  `json:"sessions"`
	Unknown        []proc.Process `json:"unknownProcesses"`
	StaleSockets   []string       `json:"staleSockets"`
}

type resultJSON struct {
	DryRun  bool          `json:"dryRun"`
	Reaped  []sessionJSON `json:"reaped"`
	Cleaned []sessionJSON `json:"cleaned"`
	Removed []string      `json:"removed"`
	Forced  int           `json:"forced"`
	FreedKB int64         `json:"freedKB"`
	Errors  []string      `json:"errors"`
}

func toSessionJSON(s reaper.Session) sessionJSON {
	j := sessionJSON{
		PID:         s.Record.PID,
		Name:        s.Record.DisplayName(),
		SessionID:   s.Record.SessionID,
		Project:     s.Record.CWD,
		Entrypoint:  s.Record.Entrypoint,
		Version:     s.Record.Version,
		RSSKB:       s.RSSKB(),
		IdleSeconds: int64(s.Idle.Seconds()),
		Verified:    s.Verified,
		Action:      string(s.Action),
		Reason:      s.Reason,
		Files:       s.Files,
	}
	if t := s.Record.Started(); !t.IsZero() {
		j.Started = &t
	} else if s.Process != nil && !s.Process.Start.IsZero() {
		t := s.Process.Start
		j.Started = &t
	}
	if !s.LastActivity.IsZero() {
		t := s.LastActivity.UTC()
		j.LastActivity = &t
	}
	return j
}

func toSessionsJSON(sessions []reaper.Session) []sessionJSON {
	out := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toSessionJSON(s))
	}
	return out
}

func newReportJSON(rep *reaper.Report) reportJSON {
	j := reportJSON{
		Now:            rep.Now.UTC(),
		ConfigDir:      rep.ConfigDir,
		MaxIdleSeconds: int64(rep.MaxIdle.Seconds()),
		Entrypoints:    rep.Entrypoints,
		Summary: summaryJSON{
			Sessions:   len(rep.Sessions),
			Reap:       rep.Count(reaper.Reap),
			Keep:       rep.Count(reaper.Keep),
			Skip:       rep.Count(reaper.Skip),
			Clean:      rep.Count(reaper.Clean),
			ResidentKB: rep.ResidentKB(),
			ReapableKB: rep.ReapableKB(),
		},
		Sessions:     toSessionsJSON(rep.Sessions),
		Unknown:      rep.Unknown,
		StaleSockets: rep.StaleSockets,
	}
	if j.Unknown == nil {
		j.Unknown = []proc.Process{}
	}
	if j.StaleSockets == nil {
		j.StaleSockets = []string{}
	}
	return j
}

func newResultJSON(res *reaper.Result) resultJSON {
	j := resultJSON{
		DryRun:  res.DryRun,
		Reaped:  toSessionsJSON(res.Reaped),
		Cleaned: toSessionsJSON(res.Cleaned),
		Removed: res.Removed,
		Forced:  res.Forced,
		FreedKB: res.FreedKB,
		Errors:  []string{},
	}
	if j.Removed == nil {
		j.Removed = []string{}
	}
	for _, err := range res.Errors {
		j.Errors = append(j.Errors, err.Error())
	}
	return j
}
