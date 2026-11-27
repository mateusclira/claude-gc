package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/mateusclira/claude-gc/internal/humanize"
	"github.com/mateusclira/claude-gc/internal/reaper"
)

const runHelp = `Usage: claude-gc run [flags]

Reaps abandoned sessions once, right now, whatever the daemon's schedule
says: sends SIGTERM to each eligible session, waits for the grace period,
then SIGKILL, and removes the files it left behind. Use --dry-run to see what
would happen without changing anything.
`

func (a *app) runOnce(args []string) int {
	var sf scanFlags
	var dryRun bool
	fs := a.flagSet("run", &sf, runHelp)
	fs.BoolVar(&dryRun, "dry-run", false, "show what would be reaped without doing it")
	fs.BoolVar(&dryRun, "n", false, "shorthand for --dry-run")
	fs.BoolVar(&sf.json, "json", false, "print the result as JSON")
	if ok, code := a.parseFlags(fs, &sf, args); !ok {
		return code
	}
	rep, err := reaper.Scan(sf.scanOptions())
	if err != nil {
		return a.fail(err)
	}
	a.warnProblems(rep.Problems)
	res := reaper.Apply(rep, reaper.ApplyOptions{
		DryRun: dryRun,
		Grace:  sf.grace.d,
		Log:    a.logger(sf.verbose),
	})
	if sf.json {
		code := a.printJSON(newResultJSON(res))
		if len(res.Errors) > 0 {
			return 1
		}
		return code
	}
	a.printResult(rep, res)
	for _, err := range res.Errors {
		fmt.Fprintf(a.stderr, "claude-gc: %v\n", err)
	}
	if len(res.Errors) > 0 {
		return 1
	}
	return 0
}

func (a *app) printResult(rep *reaper.Report, res *reaper.Result) {
	w := a.stdout
	verb := "reaped"
	if res.DryRun {
		verb = "would reap"
	}
	if len(res.Reaped) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, s := range res.Reaped {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
				verb, s.Record.PID, s.Record.DisplayName(), shortenHome(s.Record.CWD), rssColumn(s), s.Reason)
		}
		tw.Flush()
	}
	files := humanize.Plural(len(res.Removed), "leftover file")
	switch {
	case res.DryRun && len(res.Reaped) > 0:
		fmt.Fprintf(w, "Dry run: would reap %s (%s) and remove %s. Nothing was changed.\n",
			humanize.Plural(len(res.Reaped), "session"), humanize.KB(res.FreedKB), files)
	case res.DryRun:
		fmt.Fprintf(w, "Dry run: nothing to reap among %s (none idle for longer than %s).",
			humanize.Plural(len(rep.Sessions), "session"), humanize.Duration(rep.MaxIdle))
		if len(res.Removed) > 0 {
			fmt.Fprintf(w, " Would remove %s.", files)
		}
		fmt.Fprintln(w)
	case len(res.Reaped) > 0:
		fmt.Fprintf(w, "Reaped %s, freed about %s, removed %s.",
			humanize.Plural(len(res.Reaped), "session"), humanize.KB(res.FreedKB), files)
		if res.Forced > 0 {
			fmt.Fprintf(w, " %d needed SIGKILL.", res.Forced)
		}
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "Nothing to reap among %s (none idle for longer than %s).",
			humanize.Plural(len(rep.Sessions), "session"), humanize.Duration(rep.MaxIdle))
		if len(res.Removed) > 0 {
			fmt.Fprintf(w, " Removed %s.", files)
		}
		fmt.Fprintln(w)
	}
}
