package checks

import (
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func trail(name, home string, multi, org bool, mode string) inventory.Trail {
	return inventory.Trail{Name: name, ARN: "arn:aws:cloudtrail:" + home + ":123456789012:trail/" + name, HomeRegion: home,
		MultiRegion: multi, Organization: org, Logging: true, Management: mode}
}

func TestDuplicateTrails(t *testing.T) {
	run := func(region string, ts ...inventory.Trail) map[string]Finding {
		return byResource(DuplicateTrails(&inventory.Region{Name: region, Trails: ts}, opts()))
	}
	org := trail("org-trail", "us-east-1", true, true, "all")

	// A member's own trail next to the organization trail pays; the organization trail is never named.
	got := run("eu-west-1", org, trail("audit", "eu-west-1", true, false, "all"))
	f := got["audit"]
	if len(got) != 1 || f.Priced || f.Facts["duplicates"] != "org-trail" || f.Facts["management"] != "all" ||
		!strings.Contains(f.Detail, "read and write management events organization trail org-trail already delivers in every region where both log") {
		t.Fatalf("member trail beside the organization trail: %+v", got)
	}
	for _, l := range f.Fix {
		if !strings.HasPrefix(l, "#") {
			t.Errorf("an audit trail is a person's call: comments only: %q", f.Fix)
		}
	}
	// A multi-Region trail and a single-Region one: the single-Region one pays.
	got = run("eu-west-1", trail("all-regions", "us-east-1", true, false, "all"), trail("copy", "eu-west-1", false, false, "all"))
	if len(got) != 1 || got["copy"].Facts["duplicates"] != "all-regions" || !strings.Contains(got["copy"].Detail, "events trail all-regions already delivers in eu-west-1:") {
		t.Errorf("single-Region beside multi-Region: %+v", got)
	}
	// Two multi-Region trails: one pays, reported once, in its home region.
	a, b := trail("a-trail", "eu-west-1", true, false, "all"), trail("b-trail", "us-east-1", true, false, "all")
	if got := run("eu-west-1", a, b); len(got) != 0 {
		t.Errorf("b-trail pays, but it's homed in us-east-1: nothing here: %+v", got)
	}
	if got := run("us-east-1", a, b); len(got) != 1 || got["b-trail"].Facts["duplicates"] != "a-trail" {
		t.Errorf("reported in its home region: %+v", got)
	}
	// Read-only and write-only copy nothing twice; a stopped trail copies nothing.
	stopped := trail("stopped", "eu-west-1", false, false, "all")
	stopped.Logging = false
	if got := run("eu-west-1", trail("reads", "eu-west-1", false, false, "read"), trail("writes", "eu-west-1", false, false, "write"), stopped); len(got) != 0 {
		t.Errorf("no copy twice: %+v", got)
	}
	// Writes only, beside the organization trail: it pays for writes.
	if got := run("eu-west-1", org, trail("writes", "eu-west-1", false, false, "write")); !strings.Contains(got["writes"].Detail, "the write management events") {
		t.Errorf("a write-only copy: %+v", got)
	}
	// A trail whose delivery fails may bill nothing: not counted.
	failing := trail("failing", "eu-west-1", false, false, "all")
	failing.DeliveryFailing = true
	if got := run("eu-west-1", org, failing); len(got) != 0 {
		t.Errorf("a failing delivery isn't a copy: %+v", got)
	}
	// Any trail costhound couldn't read makes the region unknown.
	unread := trail("unread", "eu-west-1", false, false, "")
	unread.Unknown = "cloudtrail:GetEventSelectors: OperationNotPermittedException"
	if got := run("eu-west-1", org, trail("audit", "eu-west-1", true, false, "all"), unread); len(got) != 0 {
		t.Errorf("unknown: no finding: %+v", got)
	}
}
