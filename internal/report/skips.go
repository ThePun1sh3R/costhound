package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// Blocked is a region where calls that worked in other regions were denied.
// That is a region restriction (an organization SCP, or an IAM condition on
// aws:RequestedRegion), not a missing permission. New AWS accounts made with
// the 2026 sign-up are limited to one region this way, so a full scan would
// otherwise print a "couldn't check" line for every call in every other region.
type Blocked struct {
	Region string   `json:"region"`
	Calls  []string `json:"calls"`
}

// NotOffered is a region where some services have no regional endpoint (DNS
// NXDOMAIN on the service host) while other AWS calls in that region still
// worked. Those are not permission or network failures: AWS simply doesn't
// offer the service there.
type NotOffered struct {
	Region   string   `json:"region"`
	Services []string `json:"services"`
	// Ops are the failed calls behind Services. They stay out of the JSON but
	// keep the savings ledger treating these checks as unread here, so a DNS
	// hiccup on one endpoint can never book a fake fix.
	Ops []string `json:"-"`
}

// deniedCodes are the error codes AWS uses for "not allowed", per API family.
var deniedCodes = map[string]bool{
	"AccessDenied":          true, // ELB, most query APIs
	"AccessDeniedException": true, // JSON APIs (logs, ...)
	"UnauthorizedOperation": true, // EC2
}

// alwaysCalled are free-plan EC2 reads Load always makes in every region. If
// any of them is missing from that region's skips, the call succeeded and the
// region was reached — even when the account is empty and there are no findings.
var alwaysCalled = []string{
	"ec2:DescribeVolumes",
	"ec2:DescribeInstances",
	"ec2:DescribeAddresses",
	"ec2:DescribeSnapshots",
	"ec2:DescribeImages",
	"ec2:DescribeNatGateways",
	"ec2:DescribeVpcEndpoints",
}

// splitSkips separates region restrictions and "not offered here" from real
// skips. A denied call counts as region-blocked only when the same call worked
// in at least one other scanned region; a call denied everywhere is a missing
// permission and stays a skip. A DNS "no such host" counts as not-offered only
// when at least one other AWS call in that region succeeded (an always-called
// EC2 read is absent from that region's skips); when nothing succeeded
// (VPN/DNS down), they stay skips so the savings ledger never treats an
// unread region as clean.
func splitSkips(regions []string, skipped []inventory.Skip) (real []inventory.Skip, blocked []Blocked, notOffered []NotOffered) {
	failedIn := map[string]map[string]bool{} // call -> regions where it failed
	for _, s := range skipped {
		if failedIn[s.What] == nil {
			failedIn[s.What] = map[string]bool{}
		}
		failedIn[s.What][s.Region] = true
	}
	worksSomewhere := func(call string) bool {
		for _, r := range regions {
			if !failedIn[call][r] {
				return true
			}
		}
		return false
	}

	byRegion := map[string][]inventory.Skip{}
	var order []string
	for _, s := range skipped {
		if _, ok := byRegion[s.Region]; !ok {
			order = append(order, s.Region)
		}
		byRegion[s.Region] = append(byRegion[s.Region], s)
	}

	// A region was reached when an always-called EC2 read is not among its
	// skips: Load always attempts those, so absence means success.
	reached := map[string]bool{}
	for region, list := range byRegion {
		if region == "global" || strings.HasSuffix(region, "/global") {
			continue
		}
		failed := map[string]bool{}
		for _, s := range list {
			failed[s.What] = true
		}
		for _, c := range alwaysCalled {
			if !failed[c] {
				reached[region] = true
				break
			}
		}
	}

	blockedBy := map[string][]string{}
	offeredBy := map[string]map[string]bool{}
	opsBy := map[string][]string{}
	var offeredOrder []string
	for _, region := range order {
		for _, s := range byRegion[region] {
			if s.Region != "global" && !strings.HasSuffix(s.Region, "/global") &&
				s.Error == inventory.NoSuchHost && reached[s.Region] {
				if offeredBy[s.Region] == nil {
					offeredBy[s.Region] = map[string]bool{}
					offeredOrder = append(offeredOrder, s.Region)
				}
				offeredBy[s.Region][inventory.ServiceLabel(s.What)] = true
				opsBy[s.Region] = append(opsBy[s.Region], s.What)
				continue
			}
			if s.Region != "global" && !strings.HasSuffix(s.Region, "/global") &&
				deniedCodes[s.Error] && worksSomewhere(s.What) {
				blockedBy[s.Region] = append(blockedBy[s.Region], s.What)
				continue
			}
			real = append(real, s)
		}
	}
	for r, calls := range blockedBy {
		sort.Strings(calls)
		blocked = append(blocked, Blocked{Region: r, Calls: calls})
	}
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Region < blocked[j].Region })
	for _, r := range offeredOrder {
		names := make([]string, 0, len(offeredBy[r]))
		for n := range offeredBy[r] {
			names = append(names, n)
		}
		sort.Strings(names)
		notOffered = append(notOffered, NotOffered{Region: r, Services: names, Ops: opsBy[r]})
	}
	sort.Slice(notOffered, func(i, j int) bool { return notOffered[i].Region < notOffered[j].Region })
	return real, blocked, notOffered
}

// writeSkips prints real skips grouped by call and error, so one missing
// permission is one line, not one line per region.
func writeSkips(w io.Writer, regions []string, skipped []inventory.Skip) {
	type key struct{ what, err string }
	where := map[key][]string{}
	var order []key
	for _, s := range skipped {
		k := key{s.What, s.Error}
		if _, ok := where[k]; !ok {
			order = append(order, k)
		}
		where[k] = append(where[k], s.Region)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].what < order[j].what })
	fmt.Fprintf(w, "Couldn't check (missing permission or API error):\n")
	for _, k := range order {
		rs := where[k]
		in := strings.Join(rs, ", ")
		if len(regions) > 1 && len(rs) == len(regions) {
			in = "all regions"
		}
		fmt.Fprintf(w, "  %-44s %-22s %s\n", k.what, k.err, in)
	}
	fmt.Fprintf(w, "\n")
}

// writeBlocked prints region restrictions as one short paragraph.
func writeBlocked(w io.Writer, blocked []Blocked) {
	names := make([]string, len(blocked))
	for i, b := range blocked {
		names[i] = b.Region
	}
	fmt.Fprintf(w, "Not scanned, %d region(s) blocked for this account by a region restriction\n", len(blocked))
	fmt.Fprintf(w, "(normal for accounts limited to some regions; calls that work elsewhere were denied there):\n")
	line := " "
	for i, n := range names {
		item := " " + n
		if i < len(names)-1 {
			item += ","
		}
		if len(line)+len(item) > 78 {
			fmt.Fprintln(w, line)
			line = " "
		}
		line += item
	}
	fmt.Fprintf(w, "%s\n\n", line)
}

// WriteNotOffered prints one short line per region whose missing endpoints are
// services AWS doesn't offer there (not permission or network failures).
func WriteNotOffered(w io.Writer, notOffered []NotOffered) {
	for _, n := range notOffered {
		fmt.Fprintf(w, "%s: %s aren't offered here\n", n.Region, strings.Join(n.Services, ", "))
	}
}
