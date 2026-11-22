package checks

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func TestEmptyHostedZones(t *testing.T) {
	r := fixture()
	fs := EmptyHostedZones(r, opts())
	if len(fs) != 1 || fs[0].Key != "Z0EMPTY" || fs[0].Region != "global" || fs[0].MonthlyUSD != 0.50 || fs[0].Confidence != "medium" {
		t.Fatalf("one empty public zone, the account under 25 zones: %+v", fs)
	}
	f := fs[0]
	if !strings.Contains(f.Detail, "12 hours") || !strings.Contains(f.Detail, "first 25") || !strings.Contains(f.Detail, "Advanced hosted zone") || !strings.Contains(f.Why, "delegate") {
		t.Errorf("detail %q why %q", f.Detail, f.Why)
	}
	if len(f.Fix) != 3 || !strings.Contains(f.Fix[0], "dig NS old.example.com") || f.Fix[2] != "# 3. aws route53 delete-hosted-zone --id Z0EMPTY" {
		t.Errorf("delegation first, the delete last and commented: %q", f.Fix)
	}
	// Private and service-made zones aren't judged at all; signed, unread and
	// non-empty ones exist without a finding: parked if they had one.
	slices.Sort(r.Unjudged)
	var zones []string
	for _, u := range r.Unjudged {
		if strings.HasPrefix(u, "empty-hosted-zone|") {
			zones = append(zones, u)
		}
	}
	if strings.Join(zones, " ") != "empty-hosted-zone|Z0FULL empty-hosted-zone|Z0SIGNED" {
		t.Errorf("unjudged: %v", zones)
	}
	r = &inventory.Region{Name: "global", Zones: r.Zones}
	if fs := EmptyHostedZones(r, opts()); fs != nil || r.Unjudged != nil {
		t.Errorf("zones not listed in full: nothing judged, nothing parked: %v %v", fs, r.Unjudged)
	}
}

// Deleting k of N zones takes the $0.10 ones (past the 25th) off the bill first.
func TestEmptyHostedZonesPricedFromTheTop(t *testing.T) {
	r := &inventory.Region{Name: "global", ZonesListed: true}
	for i := 1; i <= 27; i++ {
		z := inventory.HostedZone{ID: fmt.Sprintf("Z%02d", i), Name: fmt.Sprintf("d%02d.example.com", i), Records: 5}
		if i <= 3 {
			z.Records, z.DNSSEC = 2, "none"
		}
		r.Zones = append(r.Zones, z)
	}
	fs := EmptyHostedZones(r, opts())
	sum := 0.0
	for _, f := range fs {
		sum += f.MonthlyUSD
	}
	// 27 zones: deleting any one of them saves $0.10; deleting more than 2
	// saves $0.50 each beyond that, which the Detail says (the low side).
	if len(fs) != 3 || !near(sum, 0.30) || fs[0].MonthlyUSD != 0.10 || fs[2].MonthlyUSD != 0.10 ||
		!strings.Contains(fs[0].Detail, "deleting more than 2 of the empty ones saves $0.50 each beyond that") {
		t.Errorf("each at its own saving: %v", fs)
	}
	if fs[0].Facts["zones_in_account"] != "27" {
		t.Errorf("facts: %v", fs[0].Facts)
	}
}
