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

// deniedCodes are the error codes AWS uses for "not allowed", per API family.
var deniedCodes = map[string]bool{
	"AccessDenied":          true, // ELB, most query APIs
	"AccessDeniedException": true, // JSON APIs (logs, ...)
	"UnauthorizedOperation": true, // EC2
}

// splitSkips separates region restrictions from real skips. A denied call
// counts as region-blocked only when the same call worked in at least one
// other scanned region; a call denied everywhere is a missing permission and
// stays a skip.
func splitSkips(regions []string, skipped []inventory.Skip) (real []inventory.Skip, blocked []Blocked) {
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
	byRegion := map[string][]string{}
	for _, s := range skipped {
		// A global call (Route 53, the bucket list) runs once, not per region:
		// "it worked in another region" says nothing about it.
		if s.Region != "global" && deniedCodes[s.Error] && worksSomewhere(s.What) {
			byRegion[s.Region] = append(byRegion[s.Region], s.What)
			continue
		}
		real = append(real, s)
	}
	for r, calls := range byRegion {
		sort.Strings(calls)
		blocked = append(blocked, Blocked{Region: r, Calls: calls})
	}
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Region < blocked[j].Region })
	return real, blocked
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
