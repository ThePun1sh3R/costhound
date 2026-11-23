package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func TestMoney(t *testing.T) {
	for in, want := range map[float64]string{
		0: "$0.00", 3.65: "$3.65", 999.999: "$1,000.00", 1234.5: "$1,234.50", 1234567.891: "$1,234,567.89", -12.5: "-$12.50",
	} {
		if got := Money(in); got != want {
			t.Errorf("Money(%v) = %q, want %q", in, got, want)
		}
	}
}

func sample() *Report {
	f := []checks.Finding{
		{Check: "idle-ip", Region: "us-east-1", Resource: "203.0.113.1", Detail: "d", MonthlyUSD: 3.65, Priced: true, Fix: []string{"aws ec2 release-address"}},
		{Check: "old-instance-type", Region: "eu-west-1", Resource: "i-1", Detail: "t2.micro"},
		{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1", Detail: "d", MonthlyUSD: 100, Priced: true, Fix: []string{"a", "b"}},
		{Check: "log-retention", Region: "eu-west-1", Resource: "tiny", MonthlyUSD: 0.10, Priced: true},
		{Check: "old-snapshots", Region: "eu-west-1", Resource: "5 snapshot(s)", MonthlyUSD: 2, Priced: true, Fix: []string{"1", "2", "3", "4", "5"}},
	}
	// Denied in every scanned region: a missing permission, not a region block.
	skips := []inventory.Skip{
		{Region: "us-east-1", What: "logs:DescribeLogGroups", Error: "AccessDeniedException"},
		{Region: "eu-west-1", What: "logs:DescribeLogGroups", Error: "AccessDeniedException"},
	}
	return New("v0.1.0", "123456789012", time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), []string{"us-east-1", "eu-west-1"}, f, skips, 0.5)
}

func TestNewSortsAndFilters(t *testing.T) {
	r := sample()
	var order []string
	for _, f := range r.Findings {
		order = append(order, f.Resource)
	}
	want := "vol-1,203.0.113.1,5 snapshot(s),i-1" // biggest first, unpriced last, $0.10 dropped
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
	if r.TotalMonthlyUSD != 105.65 {
		t.Errorf("total = %v, want 105.65", r.TotalMonthlyUSD)
	}
}

func TestText(t *testing.T) {
	var b bytes.Buffer
	sample().Text(&b, true)
	out := b.String()
	for _, want := range []string{
		"Account 123456789012 · 2 region(s) · 2026-11-17 12:00 UTC",
		"Estimated waste: $105.65 / month  (about $1,267.80 / year)",
		"Unattached EBS volumes", "$100.00/mo",
		"fix: a", "... and 2 more (run with -json for all of them)",
		"Not priced, worth a look:", "t2.micro",
		"Couldn't check", "logs:DescribeLogGroups", "AccessDeniedException", "all regions",
		"never changes anything",
		"costhound Pro also checks: NAT gateways with (almost) no traffic;", // titles keep their capitals
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report misses %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "tiny") {
		t.Error("finding under -min-usd was printed")
	}
	// The Team checks aren't Pro's: they get their own line.
	pro, _, _ := strings.Cut(out[strings.Index(out, "costhound Pro also checks: "):], "\n")
	if strings.Contains(pro, "Savings Plans and Reserved Instances to buy") || !strings.Contains(out, "costhound Team adds: Savings Plans and Reserved Instances to buy") {
		t.Errorf("free upsell: Team checks listed as Pro:\n%s", out)
	}
	// The run's own price note, not the built-in one, when the run set it.
	r := sample()
	r.PriceNote = "Prices are each region's on-demand prices from the AWS Price List."
	b.Reset()
	r.Text(&b, true)
	if !strings.Contains(b.String(), r.PriceNote) || strings.Contains(b.String(), PriceNote) {
		t.Errorf("text report ignores the run's price note:\n%s", b.String())
	}

	b.Reset()
	sample().Text(&b, false)
	if strings.Contains(b.String(), "fix:") {
		t.Error("-no-fix still printed fixes")
	}
}

func TestTextEmpty(t *testing.T) {
	var b bytes.Buffer
	New("v", "1", time.Now(), []string{"x"}, nil, nil, 0).Text(&b, true)
	if !strings.Contains(b.String(), "No waste found") {
		t.Errorf("empty report:\n%s", b.String())
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	if err := sample().JSON(&b); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if back.Account != "123456789012" || len(back.Findings) != 4 || len(back.Findings[2].Fix) != 5 || back.Skipped[0].Error != "AccessDeniedException" {
		t.Errorf("round trip = %+v", back)
	}
	// Empty lists must be [] not null, so scripts can iterate safely.
	b.Reset()
	New("v", "1", time.Now(), nil, nil, nil, 0).JSON(&b)
	if !strings.Contains(b.String(), `"findings": []`) || !strings.Contains(b.String(), `"skipped": []`) || !strings.Contains(b.String(), `"blocked_regions": []`) {
		t.Errorf("empty JSON = %s", b.String())
	}
}

// The shape seen on a real 2026 "new sign-up" account limited to eu-north-1:
// every call denied in other regions, except logs in us-east-1.
func TestRegionRestrictionIsOneParagraph(t *testing.T) {
	calls := []string{"ec2:DescribeVolumes", "ec2:DescribeImages", "elasticloadbalancing:DescribeLoadBalancers", "logs:DescribeLogGroups"}
	code := func(call string) string {
		switch {
		case strings.HasPrefix(call, "ec2:"):
			return "UnauthorizedOperation"
		case strings.HasPrefix(call, "logs:"):
			return "AccessDeniedException"
		}
		return "AccessDenied"
	}
	var skips []inventory.Skip
	for _, c := range calls {
		skips = append(skips, inventory.Skip{Region: "ap-south-1", What: c, Error: code(c)})
		if c != "logs:DescribeLogGroups" {
			skips = append(skips, inventory.Skip{Region: "us-east-1", What: c, Error: code(c)})
		}
	}
	// A real missing permission (denied in the home region too) and a
	// non-permission error must still be reported as skips.
	skips = append(skips,
		inventory.Skip{Region: "eu-north-1", What: "ec2:DescribeImages", Error: "UnauthorizedOperation"},
		inventory.Skip{Region: "eu-north-1", What: "ec2:DescribeSnapshots", Error: "RequestLimitExceeded"},
	)
	r := New("v", "1", time.Now(), []string{"eu-north-1", "us-east-1", "ap-south-1"}, nil, skips, 0)

	if len(r.BlockedRegions) != 2 || r.BlockedRegions[0].Region != "ap-south-1" || r.BlockedRegions[1].Region != "us-east-1" {
		t.Fatalf("blocked = %+v", r.BlockedRegions)
	}
	if got := len(r.BlockedRegions[0].Calls); got != 3 { // DescribeImages is denied everywhere, so it isn't a region block
		t.Errorf("ap-south-1 blocked calls = %d, want 3: %v", got, r.BlockedRegions[0].Calls)
	}
	var real []string
	for _, s := range r.Skipped {
		real = append(real, s.Region+" "+s.What+" "+s.Error)
	}
	want := "ap-south-1 ec2:DescribeImages UnauthorizedOperation,eu-north-1 ec2:DescribeImages UnauthorizedOperation,eu-north-1 ec2:DescribeSnapshots RequestLimitExceeded,us-east-1 ec2:DescribeImages UnauthorizedOperation"
	if got := strings.Join(real, ","); got != want {
		t.Errorf("real skips =\n  %s\nwant\n  %s", got, want)
	}

	var b bytes.Buffer
	r.Text(&b, true)
	out := b.String()
	for _, w := range []string{"2 region(s) blocked for this account by a region restriction", "ap-south-1, us-east-1", "ec2:DescribeImages", "all regions", "RequestLimitExceeded"} {
		if !strings.Contains(out, w) {
			t.Errorf("text misses %q\n%s", w, out)
		}
	}
	if n := strings.Count(out, "ec2:DescribeImages"); n != 1 {
		t.Errorf("a permission denied in every region should be one line, got %d\n%s", n, out)
	}
}

func TestSingleRegionDeniedStaysASkip(t *testing.T) {
	// With nothing to compare against, a denial can't be called a region block.
	skips := []inventory.Skip{{Region: "us-east-1", What: "ec2:DescribeVolumes", Error: "UnauthorizedOperation"}}
	r := New("v", "1", time.Now(), []string{"us-east-1"}, nil, skips, 0)
	if len(r.BlockedRegions) != 0 || len(r.Skipped) != 1 {
		t.Fatalf("blocked=%+v skipped=%+v", r.BlockedRegions, r.Skipped)
	}
}

func TestPriceNoteFor(t *testing.T) {
	if n := PriceNoteFor(nil); !strings.Contains(n, "us-east-1") || !strings.Contains(n, "Pro uses each region's own list prices") {
		t.Errorf("free: %s", n)
	}
	all := map[string]string{"eu-west-1": "AWS Price List (fetched 2026-11-19)", "eu-north-1": "AWS Price List (fetched 2026-11-19)"}
	if n := PriceNoteFor(all); !strings.Contains(n, "AWS Price List (fetched 2026-11-19)") || strings.Contains(n, "except") {
		t.Errorf("all regional: %s", n)
	}
	all["ap-south-1"] = "built-in us-east-1 list prices (Price List unavailable: AccessDenied)"
	if n := PriceNoteFor(all); !strings.Contains(n, "except ap-south-1") {
		t.Errorf("mixed: %s", n)
	}
}

func TestOwnersInText(t *testing.T) {
	fs := []checks.Finding{
		{ID: "ch_1", Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1", MonthlyUSD: 40, Priced: true, Owner: "Payments", OwnerFrom: "tag Team"},
		{ID: "ch_2", Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-2", MonthlyUSD: 10, Priced: true, Owner: "payments", OwnerFrom: "tag owner"},
		{ID: "ch_3", Check: "idle-ip", Region: "eu-west-1", Resource: "1.2.3.4", MonthlyUSD: 3.65, Priced: true, OwnerFrom: "none"},
		{ID: "ch_4", Check: "lambda-x86", Region: "eu-west-1", Resource: "2 function(s) on x86_64", MonthlyUSD: 5, Priced: true}, // several resources
		{Check: "log-retention", Region: "eu-west-1", Resource: "/app", OwnerFrom: "none"},
	}
	var b strings.Builder
	New("v", "1", time.Now(), []string{"eu-west-1"}, fs, nil, 0).Text(&b, false)
	out := b.String()
	for _, want := range []string{
		"Unowned: $3.65 / month of it on 2 resource(s) with no owner tag",
		"· owner Payments (tag Team)",
		"· owner payments (tag owner)",
		"confidence  · no owner tag",
		"By owner:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report lacks %q:\n%s", want, out)
		}
	}
	rows := ByOwner(fs)
	if len(rows) != 2 || rows[0].Owner != "Payments" || rows[0].Findings != 2 || rows[0].MonthlyUSD != 50 || rows[1].Owner != NoOwner || rows[1].Findings != 2 {
		t.Errorf("owners merge whatever the case, and the summed-up finding isn't anyone's: %+v", rows)
	}
	if strings.Count(out, "· owner ")+strings.Count(out, "· no owner tag") != 3 {
		t.Errorf("only the three priced findings about one resource name an owner:\n%s", out)
	}
}

// A long check title widens the By check column instead of pushing its row
// out of line.
func TestTextByCheckAligns(t *testing.T) {
	at := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	fs := []checks.Finding{
		{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1", MonthlyUSD: 10, Priced: true},
		{Check: "cloudtrail-duplicate", Region: "eu-west-1", Resource: "trail-2", Detail: "unpriced"},
	}
	var b bytes.Buffer
	New("v", "123456789012", at, []string{"eu-west-1"}, fs, nil, 0).Text(&b, false)
	col := map[string]int{}
	for _, line := range strings.Split(b.String(), "\n") {
		for _, id := range []string{"unattached-ebs", "cloudtrail-duplicate"} {
			if title := checks.Title(id); strings.HasPrefix(line, "  "+title+" ") {
				col[id] = len(line) - len(strings.TrimLeft(line[len(title)+2:], " ")) // where the count starts
			}
		}
	}
	if len(col) != 2 || col["unattached-ebs"] != col["cloudtrail-duplicate"] {
		t.Errorf("the count column doesn't line up: %v\n%s", col, b.String())
	}
}
