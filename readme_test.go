package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

// Every check and every flag must be documented in the README, so the page
// people read before buying never falls behind the tool.
func TestReadmeListsEveryCheckAndFlag(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	for _, c := range checks.All {
		if !strings.Contains(readme, "| "+c.Title+" |") {
			t.Errorf("README has no table row for check %q (%s)", c.Title, c.ID)
		}
	}
	for _, f := range []string{"profile", "regions", "check", "json", "no-fix", "min-usd", "snapshot-age", "stopped-days", "secret-days",
		"min-log-gib", "concurrency", "timeout", "license", "html", "usage-days", "min-ingest-gib", "no-history", "history-dir",
		"org", "org-role", "org-accounts", "slack-webhook", "fail-over", "fail-on-new", "fail-on-unowned", "redact", "redact-full", "redact-key", "redact-map", "sign", "sign-key", "ics", "tf-state", "ignore-file",
		"out", "from", "account", "only", "checks", "min-confidence"} { // the last row: costhound plan
		if !strings.Contains(readme, "`-"+f+"`") {
			t.Errorf("README flag table misses -%s", f)
		}
	}
}

// readmeChecksRow pulls Free, Pro (+N) and Team (+M) counts from the plans table.
var readmeChecksRow = regexp.MustCompile(`(?m)^\| \*\*Finds\*\* \| (\d+) classic AWS waste checks:.*?\| \+ (\d+) AWS checks .*?\| \+ (\d+) checks:`)

// The plan table counts the checks added by each tier.
func TestReadmeCheckCounts(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	code := map[string]int{}
	for _, c := range checks.All {
		code[c.Plan]++
	}
	m := readmeChecksRow.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("README plan table has no Checks row with Free, Pro and Team counts")
	}
	for i, plan := range []string{license.PlanFree, license.PlanPro, license.PlanTeam} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			t.Fatalf("%s: bad README count %q", plan, m[i+1])
		}
		if n != code[plan] {
			t.Errorf("%s: README has %d, code has %d", plan, n, code[plan])
		}
	}
}

// readmeShowSummary is a collapsed check list's heading, e.g.
// "<summary><b>Show the 88 Pro checks</b></summary>".
var readmeShowSummary = regexp.MustCompile(`<summary><b>Show the (\d+) ([^<]*?)checks</b></summary>`)

// Each "Show the N ... checks" heading must match the table rows under it
// (header and separator rows not counted), so a heading can't go stale.
func TestReadmeShowCountsMatchRows(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	locs := readmeShowSummary.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		t.Fatal(`README has no "Show the N ... checks" headings`)
	}
	for _, loc := range locs {
		want, _ := strconv.Atoi(s[loc[2]:loc[3]])
		label := strings.TrimSpace(s[loc[4]:loc[5]])
		rest := s[loc[1]:]
		end := strings.Index(rest, "</details>")
		if end < 0 {
			t.Fatalf("%q checks: no </details> after the heading", label)
		}
		lines := strings.Split(rest[:end], "\n")
		rows := 0
		for i, l := range lines {
			if !strings.HasPrefix(l, "|") || strings.HasPrefix(l, "|---") || strings.HasPrefix(l, "| ---") {
				continue
			}
			if i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "|---") || strings.HasPrefix(lines[i+1], "| ---")) {
				continue // a header row
			}
			rows++
		}
		if rows != want {
			t.Errorf("%q checks: heading says %d, the table under it has %d rows", label, want, rows)
		}
	}
}
