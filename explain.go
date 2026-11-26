package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

// explain prints everything costhound knows about one finding:
// `costhound explain <id> [-from report.json]`. Without -from it looks in
// the saved AWS runs and (Pro/Team) the latest saved Azure and GCP runs.
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

	if *from != "" {
		b, err := os.ReadFile(*from)
		if err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
		switch detectReportKind(b) {
		case "azure", "gcp":
			return explainCloudFromJSON(b, id, out)
		case "aws", "":
			var r report.Report
			if err := json.Unmarshal(b, &r); err != nil {
				fmt.Fprintf(os.Stderr, "costhound: %s is not a costhound JSON report: %v\n", *from, err)
				return 1
			}
			if f, ack, ok := findingInAWSReport(&r, id); ok {
				writeExplanation(out, &r, f, ack)
				return 0
			}
			fmt.Fprintf(os.Stderr, "costhound: %s isn't in %s; looked in that AWS report (IDs are shown under each finding)\n", id, *from)
			return 2
		}
	}

	reps := savedReports(*dir)
	for _, r := range reps {
		if f, ack, ok := findingInAWSReport(r, id); ok {
			writeExplanation(out, r, f, ack)
			return 0
		}
	}
	if code := explainCloudFromHistory(*dir, id, out); code >= 0 {
		return code
	}
	looked := "the saved AWS, Azure and GCP runs"
	if hist := historyDirLabel(*dir); hist != "" {
		looked += " under " + hist
	}
	if len(reps) == 0 {
		fmt.Fprintf(os.Stderr, "costhound: %s isn't in %s; pass -from report.json (made with costhound -json)\n", id, looked)
	} else {
		fmt.Fprintf(os.Stderr, "costhound: %s isn't in %s; IDs are shown under each finding\n", id, looked)
	}
	return 2
}

// detectReportKind tells AWS / Azure / GCP reports apart the way summary and
// the dashboard do: cloud field, else subscription_ids / project_ids.
func detectReportKind(b []byte) string {
	var peek struct {
		Cloud           string   `json:"cloud"`
		Account         string   `json:"account"`
		TenantID        string   `json:"tenant_id"`
		SubscriptionIDs []string `json:"subscription_ids"`
		ProjectIDs      []string `json:"project_ids"`
	}
	if json.Unmarshal(b, &peek) != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(peek.Cloud)) {
	case "azure":
		return "azure"
	case "gcp":
		return "gcp"
	case "aws":
		return "aws"
	}
	if peek.TenantID != "" || len(peek.SubscriptionIDs) > 0 {
		return "azure"
	}
	if len(peek.ProjectIDs) > 0 {
		return "gcp"
	}
	if peek.Account != "" {
		return "aws"
	}
	return ""
}

func findingInAWSReport(r *report.Report, id string) (checks.Finding, *report.Acknowledged, bool) {
	for _, f := range r.Findings {
		if f.ID == id {
			return f, nil, true
		}
	}
	for i := range r.Acknowledged {
		if r.Acknowledged[i].ID == id {
			a := r.Acknowledged[i]
			return a.Finding, &a, true
		}
	}
	return checks.Finding{}, nil, false
}

func writeExplanation(w io.Writer, r *report.Report, f checks.Finding, ack *report.Acknowledged) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	plan := "free"
	title := checks.Title(f.Check)
	for _, c := range checks.All {
		if c.ID == f.Check {
			plan = c.Plan
			break
		}
	}
	account := f.Account
	if account == "" {
		account = r.Account
	}
	p("%s · %s (%s check)\n", f.ID, title, plan)
	p("Account %s · %s · %s\n", account, f.Region, f.Resource)
	if f.Priced {
		p("Cost: %s/month (about %s/year)\n", report.Money(f.MonthlyUSD), report.Money(f.MonthlyUSD*12))
	} else {
		p("Cost: not priced\n")
	}
	writePriceEvidence(w, f)
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
	writeFacts(w, f)
	if m, ok := checks.Meta[f.Check]; ok && len(m.Sources) > 0 {
		p("\nRead with (read-only calls): %s\n", strings.Join(m.Sources, ", "))
	}
	if len(f.Fix) > 0 {
		p("\nFix (review it, then run it yourself; costhound never changes anything):\n")
		for _, c := range f.Fix {
			p("  %s\n", c)
		}
	}
	if hint := explainFixHint(f.Check); hint != "" {
		p("%s\n", hint)
	}
	if ack == nil {
		p("\nTo acknowledge it instead, add this to .costhound.yaml:\n")
		p("ignore:\n  - id: %s\n    reason: <why you keep it>\n    owner: <team or person>\n    until: %s\n", f.ID, time.Now().AddDate(0, 3, 0).Format("2006-01-02"))
	}
}

func writePriceEvidence(w io.Writer, f checks.Finding) {
	var lines []string
	for _, e := range f.Evidence {
		el := strings.ToLower(e)
		if strings.Contains(el, "retail meter") || strings.Contains(el, "retail sku") ||
			strings.Contains(el, "retail effective") || strings.Contains(el, "catalog") ||
			strings.Contains(el, "list price") || strings.HasPrefix(el, "sku ") {
			lines = append(lines, e)
		}
	}
	if sku := f.Facts["sku"]; sku != "" {
		lines = append(lines, "sku "+sku)
	}
	if len(lines) == 0 && !f.Priced {
		return
	}
	if f.Region != "" {
		lines = append(lines, "region "+f.Region)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "Price evidence:")
	for _, line := range lines {
		fmt.Fprintf(w, "  - %s\n", line)
	}
}

func writeFacts(w io.Writer, f checks.Finding) {
	if len(f.Facts) == 0 {
		return
	}
	keys := make([]string, 0, len(f.Facts))
	for k := range f.Facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "Facts:")
	for _, k := range keys {
		fmt.Fprintf(w, "  - %s: %s\n", k, f.Facts[k])
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
