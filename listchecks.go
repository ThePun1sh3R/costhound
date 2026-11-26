package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

// checkRow is one registered check for costhound checks.
type checkRow struct {
	Cloud     string `json:"cloud"`
	ID        string `json:"id"`
	Plan      string `json:"plan"`
	Preview   bool   `json:"preview,omitempty"`
	Finds     string `json:"finds"`
	Pricing   string `json:"pricing"`
	FixScript bool   `json:"fix_script"`
}

func runChecksList(args []string) int {
	return checksListTo(args, os.Stdout, os.Stderr)
}

func checksListTo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("costhound checks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "costhound checks: list every check costhound has, from the code registries (no license needed).\n\nUsage: costhound checks [-cloud aws|azure|gcp] [-plan free|pro|team] [-json] [-markdown]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	cloud := fs.String("cloud", "", "only this cloud: aws, azure or gcp")
	plan := fs.String("plan", "", "only checks whose cheapest plan is free, pro or team")
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	asMD := fs.Bool("markdown", false, "print a markdown table (for the README or site)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "costhound checks: unexpected arguments (see costhound checks -h)")
		return 2
	}
	if *asJSON && *asMD {
		fmt.Fprintln(stderr, "costhound checks: use only one of -json and -markdown")
		return 2
	}
	c := strings.ToLower(strings.TrimSpace(*cloud))
	switch c {
	case "", "aws", "azure", "gcp":
	default:
		fmt.Fprintln(stderr, "costhound checks: -cloud must be aws, azure or gcp")
		return 2
	}
	p := strings.ToLower(strings.TrimSpace(*plan))
	switch p {
	case "", license.PlanFree, license.PlanPro, license.PlanTeam:
	default:
		fmt.Fprintln(stderr, "costhound checks: -plan must be free, pro or team")
		return 2
	}

	rows := registeredChecks()
	var out []checkRow
	for _, r := range rows {
		if c != "" && r.Cloud != c {
			continue
		}
		if p != "" && r.Plan != p {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cloud != out[j].Cloud {
			return cloudOrder(out[i].Cloud) < cloudOrder(out[j].Cloud)
		}
		if out[i].Plan != out[j].Plan {
			return license.Rank(out[i].Plan) < license.Rank(out[j].Plan)
		}
		return out[i].ID < out[j].ID
	})

	switch {
	case *asJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "costhound checks: %v\n", err)
			return 1
		}
	case *asMD:
		writeChecksMarkdown(stdout, out)
	default:
		writeChecksText(stdout, out)
	}
	return 0
}

func cloudOrder(c string) int {
	switch c {
	case "aws":
		return 0
	case "azure":
		return 1
	case "gcp":
		return 2
	}
	return 9
}

func writeChecksText(w io.Writer, rows []checkRow) {
	fmt.Fprintf(w, "costhound checks · %d check(s)\n\n", len(rows))
	for _, r := range rows {
		plan := r.Plan
		if r.Preview {
			plan += " (preview)"
		}
		fix := "no"
		if r.FixScript {
			fix = "yes"
		}
		fmt.Fprintf(w, "%s  %s  %s  %s  pricing=%s  fix_script=%s\n",
			r.Cloud, r.ID, plan, r.Finds, r.Pricing, fix)
	}
}

func writeChecksMarkdown(w io.Writer, rows []checkRow) {
	fmt.Fprintln(w, "| Cloud | ID | Plan | What it finds | Pricing | Fix script |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, r := range rows {
		plan := r.Plan
		if r.Preview {
			plan += " (preview)"
		}
		fix := "no"
		if r.FixScript {
			fix = "yes"
		}
		fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s | %s |\n",
			r.Cloud, r.ID, plan, mdCell(r.Finds), r.Pricing, fix)
	}
}

func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// awsCheckRows lists AWS checks from checks.All. freeOnly keeps only free-plan
// entries (source builds); the official build lists every plan.
func awsCheckRows(freeOnly bool) []checkRow {
	var out []checkRow
	for _, c := range checks.All {
		if freeOnly && c.Plan != license.PlanFree {
			continue
		}
		out = append(out, checkRow{
			Cloud:     "aws",
			ID:        c.ID,
			Plan:      c.Plan,
			Finds:     c.Title,
			Pricing:   checks.PricingOf(c.ID),
			FixScript: awsFixScript(c.ID),
		})
	}
	return out
}
