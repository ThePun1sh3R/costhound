package main

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
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
	for _, f := range []string{"profile", "regions", "json", "no-fix", "min-usd", "snapshot-age", "stopped-days", "secret-days",
		"min-log-gib", "concurrency", "timeout", "license", "html", "usage-days", "min-ingest-gib", "no-history", "history-dir",
		"org", "org-role", "org-accounts", "slack-webhook", "fail-over", "fail-on-new", "fail-on-unowned", "redact", "redact-key", "redact-map", "sign", "sign-key", "ics", "tf-state", "ignore-file",
		"out", "from", "account", "only", "checks", "min-confidence"} { // the last row: costhound plan
		if !strings.Contains(readme, "`-"+f+"`") {
			t.Errorf("README flag table misses -%s", f)
		}
	}
}

// The plan table's free count is the real one: it was left at 34 when two
// free checks arrived.
func TestReadmeFreeCheckCount(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range checks.All {
		if c.Plan == "free" {
			n++
		}
	}
	if want := "| Checks | " + strconv.Itoa(n) + " classic waste checks |"; !strings.Contains(string(b), want) {
		t.Errorf("README's plan table must say %q", want)
	}
}
