package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

const statusHelp = `Usage: claude-gc status [flags]

Lists every Claude Code session found in the config directory together with
its process and the action claude-gc would take. Nothing is changed.
`

func (a *app) status(args []string) int {
	var sf scanFlags
	fs := a.flagSet("status", &sf, statusHelp)
	fs.BoolVar(&sf.json, "json", false, "print the report as JSON")
	if ok, code := a.parseFlags(fs, &sf, args); !ok {
		return code
	}
	rep, err := reaper.Scan(sf.scanOptions())
	if err != nil {
		return a.fail(err)
	}
	a.warnProblems(rep.Problems)
	if sf.json {
		return a.printJSON(newReportJSON(rep))
	}
	a.printReport(rep)
	return 0
}

func (a *app) printReport(rep *reaper.Report) {
	w := a.stdout
	if len(rep.Sessions) == 0 {
		fmt.Fprintf(w, "No Claude Code sessions found (looked in %s).\n", shortenHome(rep.ConfigDir))
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PID\tNAME\tPROJECT\tRSS\tSTARTED\tLAST ACTIVITY\tACTION")
		for _, s := range rep.Sessions {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
				s.Record.PID,
				s.Record.DisplayName(),
				shortenHome(s.Record.CWD),
				rssColumn(s),
				startedColumn(s, rep.Now),
				activityColumn(s, rep.Now),
				a.actionColumn(s))
		}
		tw.Flush()
		fmt.Fprintln(w)
		fmt.Fprintln(w, summaryLine(rep))
	}
	if n := len(rep.StaleSockets); n > 0 {
		fmt.Fprintf(w, "%s left behind by exited sessions.\n", humanize.Plural(n, "stale socket file"))
	}
	if n := len(rep.Unknown); n > 0 {
		pids := make([]string, 0, n)
		for _, p := range rep.Unknown {
			pids = append(pids, strconv.Itoa(p.PID))
		}
		fmt.Fprintf(w, "%s without a session record, left alone: %s\n",
			humanize.Plural(n, "claude process"), strings.Join(pids, ", "))
	}
	if a.tty && rep.Count(reaper.Reap) > 0 {
		fmt.Fprintln(w, "\nRun `claude-gc run` to reap them, or `claude-gc service install` to keep this tidy automatically.")
	}
}

func rssColumn(s reaper.Session) string {
	if s.Process == nil {
		return "-"
	}
	return humanize.KB(s.Process.RSSKB)
}

func startedColumn(s reaper.Session, now time.Time) string {
	t := s.Record.Started()
	if t.IsZero() && s.Process != nil {
		t = s.Process.Start
	}
	return humanize.Ago(t, now)
}

func activityColumn(s reaper.Session, now time.Time) string {
	if s.LastActivity.IsZero() {
		return "none"
	}
	return humanize.Ago(s.LastActivity, now)
}

func (a *app) actionColumn(s reaper.Session) string {
	switch s.Action {
	case reaper.Reap:
		return a.paint("33", "reap")
	case reaper.Keep:
		return a.paint("32", "keep")
	default:
		return a.paint("2", fmt.Sprintf("%s (%s)", s.Action, s.Reason))
	}
}

func summaryLine(rep *reaper.Report) string {
	parts := []string{
		humanize.Plural(len(rep.Sessions), "session"),
		humanize.KB(rep.ResidentKB()) + " resident",
	}
	if n := rep.Count(reaper.Reap); n > 0 {
		parts = append(parts, fmt.Sprintf("%d to reap (%s, idle > %s)", n, humanize.KB(rep.ReapableKB()), humanize.Duration(rep.MaxIdle)))
	}
	if n := rep.Count(reaper.Keep); n > 0 {
		parts = append(parts, fmt.Sprintf("%d active", n))
	}
	if n := rep.Count(reaper.Skip); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", n))
	}
	if n := rep.Count(reaper.Clean); n > 0 {
		parts = append(parts, humanize.Plural(n, "stale record"))
	}
	return strings.Join(parts, " · ")
}
