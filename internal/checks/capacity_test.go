package checks

import (
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

func reservation(id string, change func(*inventory.CapacityReservation)) inventory.CapacityReservation {
	cr := inventory.CapacityReservation{ID: id, Type: "m5.large", Platform: "Linux/UNIX", AZ: "eu-west-1a", Total: 4, Available: 2,
		State: "active", Kind: "default", Start: *ago(60), Billed: true, Owned: true, Owner: "123456789012"}
	if change != nil {
		change(&cr)
	}
	return cr
}

func TestUnusedCapacityReservations(t *testing.T) {
	m5, _ := pricing.EC2("m5.large")
	r := &inventory.Region{Name: "eu-west-1", CapacityListed: true, CapacityReservations: []inventory.CapacityReservation{
		reservation("cr-targeted", nil),
		reservation("cr-open", func(c *inventory.CapacityReservation) { c.Open = true }),
		reservation("cr-young", func(c *inventory.CapacityReservation) { c.Start = *ago(0) }),
		reservation("cr-shared", func(c *inventory.CapacityReservation) { c.Billed = false }),
		reservation("cr-block", func(c *inventory.CapacityReservation) { c.Kind = "capacity-block" }),
		reservation("cr-fleet", func(c *inventory.CapacityReservation) { c.Elsewhere = "a Capacity Reservation Fleet manages it" }),
		reservation("cr-full", func(c *inventory.CapacityReservation) { c.Available = 0 }),
		reservation("cr-gone", func(c *inventory.CapacityReservation) { c.State = "cancelled" }),
		reservation("cr-windows", func(c *inventory.CapacityReservation) { c.Platform = "Windows" }),
		reservation("cr-committed", func(c *inventory.CapacityReservation) {
			c.Total, c.Available, c.Committed, c.CommitEnd = 10, 5, 8, *ago(-20)
		}),
		reservation("cr-held", func(c *inventory.CapacityReservation) { c.Committed, c.CommitEnd = 4, *ago(-20) }),
		reservation("cr-ending", func(c *inventory.CapacityReservation) { c.End = *ago(-10) }),
		reservation("cr-expired", func(c *inventory.CapacityReservation) { c.State = "expired" }),
		reservation("cr-billed-here", func(c *inventory.CapacityReservation) { c.Owned, c.Owner = false, "222222222222" }),
	}}
	got := map[string]Finding{}
	for _, f := range UnusedCapacityReservations(r, opts()) {
		got[f.Key] = f
	}
	want := []string{"cr-targeted", "cr-open", "cr-windows", "cr-committed", "cr-held", "cr-ending", "cr-billed-here"}
	if len(got) != len(want) {
		t.Fatalf("findings: %v", keysOf(got))
	}
	f := got["cr-targeted"]
	if f.Confidence != "medium" || !near(f.MonthlyUSD, 2*m5.Hour*pricing.HoursPerMonth) || !strings.Contains(f.Detail, "2 of its 4 instances unused now") ||
		!strings.Contains(f.Why, "spare commitment") || f.Resource != "cr-targeted (m5.large, eu-west-1a)" {
		t.Errorf("targeted: %+v", f)
	}
	if !strings.Contains(strings.Join(f.Fix, "\n"), "--instance-count 2") || !strings.HasPrefix(f.Fix[len(f.Fix)-1], "# or, if nothing needs it") {
		t.Errorf("fix: %q", f.Fix)
	}
	if got["cr-open"].Confidence != "low" || !strings.Contains(got["cr-windows"].Detail, "at least") {
		t.Errorf("open: low; Windows at the Linux price is at least: %+v %+v", got["cr-open"], got["cr-windows"])
	}
	if c := got["cr-committed"]; !near(c.MonthlyUSD, 2*m5.Hour*pricing.HoursPerMonth) || !strings.Contains(c.Detail, "can't go below 8 instances") ||
		strings.Contains(strings.Join(c.Fix, " "), "cancel-capacity-reservation") {
		t.Errorf("inside its commitment, only what's above it: %+v", c)
	}
	if h := got["cr-held"]; h.Priced || !strings.Contains(h.Detail, "not priced: it can't go below its commitment of 4") {
		t.Errorf("all of it committed: %+v", h)
	}
	if e := got["cr-ending"]; !near(e.MonthlyUSD, 2*m5.Hour*240) || !strings.Contains(e.Detail, "for the 240 hours it has left") || e.Facts["ends"] == "never" {
		t.Errorf("ends in 10 days: %+v", e)
	}
	// Everything that exists without a finding is parked; a cancelled one isn't.
	u := strings.Join(r.Unjudged, " ")
	for _, id := range []string{"cr-young", "cr-shared", "cr-block", "cr-fleet", "cr-full"} {
		if !strings.Contains(u, "unused-capacity-reservation|"+id) {
			t.Errorf("%s: not parked: %v", id, r.Unjudged)
		}
	}
	if strings.Contains(u, "cr-gone") || !strings.Contains(u, "cr-expired") {
		t.Errorf("cancelled: gone, the fix; expired: ended by itself, parked: %v", r.Unjudged)
	}
	if b := got["cr-billed-here"]; b.Priced || !strings.Contains(b.Detail, "account 222222222222 owns it") || strings.Contains(strings.Join(b.Fix, " "), "aws ec2") {
		t.Errorf("billed here, owned elsewhere: unpriced, ask the owner: %+v", b)
	}
	if fs := UnusedCapacityReservations(&inventory.Region{Name: "eu-west-1"}, opts()); fs != nil {
		t.Errorf("not listed: unknown: %v", fs)
	}
}
