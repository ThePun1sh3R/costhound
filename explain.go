package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

// explain prints everything costhound knows about one finding:
// `costhound explain <id> [-from report.json]`. Without -from it looks in
// the saved runs (paid builds keep them).
func explain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("costhound explain", flag.ContinueOnError)
	from := fs.String("from", "", "a JSON report (costhound -json > report.json)")
	dir := fs.String("history-dir", "", "saved runs (default: <config dir>/costhound/runs)")
	id := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" && fs.NArg() > 0 {
		id = fs.Arg(0)
	}
	if !strings.HasPrefix(id, "ch_") {
		fmt.Fprintln(os.Stderr, "usage: costhound explain <finding id, ch_...> [-from report.json]")
		return 2
	}
	var reps []*report.Report
	if *from != "" {
		b, err := os.ReadFile(*from)
		if err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
		var r report.Report
		if err := json.Unmarshal(b, &r); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %s is not a costhound JSON report: %v\n", *from, err)
			return 1
		}
		reps = append(reps, &r)
	} else {
		reps = savedReports(*dir)
	}
	for _, r := range reps {
		for _, f := range r.Findings {
			if f.ID == id {
				writeExplanation(out, r, f, nil)
				return 0
			}
		}
		for _, a := range r.Acknowledged {
			if a.ID == id {
				writeExplanation(out, r, a.Finding, &a)
				return 0
			}
		}
	}
	if *from == "" && len(reps) == 0 {
		fmt.Fprintln(os.Stderr, "costhound: no saved runs to look in; pass -from report.json (made with costhound -json)")
	} else {
		fmt.Fprintf(os.Stderr, "costhound: %s isn't in that report; IDs are shown under each finding\n", id)
	}
	return 1
}

func writeExplanation(w io.Writer, r *report.Report, f checks.Finding, ack *report.Acknowledged) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	plan := "free"
	for _, c := range checks.All {
		if c.ID == f.Check {
			plan = c.Plan
		}
	}
	account := f.Account
	if account == "" {
		account = r.Account
	}
	p("%s · %s (%s check)\n", f.ID, checks.Title(f.Check), plan)
	p("Account %s · %s · %s\n", account, f.Region, f.Resource)
	if f.Priced {
		p("Cost: %s/month (about %s/year)\n", report.Money(f.MonthlyUSD), report.Money(f.MonthlyUSD*12))
	} else {
		p("Cost: not priced\n")
	}
	p("Confidence: %s: %s\n", f.Confidence, f.Why)
	if ack != nil {
		p("Acknowledged: %s", orDash(ack.Reason))
		if ack.Owner != "" {
			p(" (owner %s)", ack.Owner)
		}
		if ack.Until != "" {
			p(", until %s", ack.Until)
		}
		p("\n")
	}
	p("\nWhat costhound saw (scan of %s):\n", r.ScannedAt.UTC().Format("2006-01-02 15:04 UTC"))
	for _, e := range f.Evidence {
		p("  - %s\n", e)
	}
	p("  - %s\n", f.Detail)
	if m, ok := checks.Meta[f.Check]; ok && len(m.Sources) > 0 {
		p("\nRead with (read-only calls): %s\n", strings.Join(m.Sources, ", "))
	}
	if len(f.Fix) > 0 {
		p("\nFix (review it, then run it yourself; costhound never changes anything):\n")
		for _, c := range f.Fix {
			p("  %s\n", c)
		}
	}
	if ack == nil {
		p("\nTo acknowledge it instead, add this to .costhound.yaml:\n")
		p("ignore:\n  - id: %s\n    reason: <why you keep it>\n    owner: <team or person>\n    until: %s\n", f.ID, time.Now().AddDate(0, 3, 0).Format("2006-01-02"))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
