package checks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// DuplicateTrails finds trails that deliver a second copy of management
// events another trail already delivers in this region. CloudTrail docs
// (cloudtrail-trail-manage-costs.html): the first copy of management events
// in each region is free; a member account's own trail that collects "the
// same management events as the Organizations trail is collecting a second
// copy of events. The account is charged for the second copy", and next to
// a multi-Region trail, "the trail in the single Region is delivering a
// second copy". Every copy after the first costs $2.00 per 100,000 events;
// costhound can't count the events (that needs Cost Explorer or CloudTrail
// Lake), so it's unpriced.
//
// Read and write events are counted apart (a read-only and a write-only
// trail copy nothing twice). The free copy is the organization trail, then
// a multi-Region trail, then the first by name; each trail that pays is
// reported once, in its home region, and an organization trail never is.
// A trail whose status or selectors couldn't be read makes the region
// unknown: no finding.
func DuplicateTrails(r *inventory.Region, o Options) []Finding {
	var live []inventory.Trail
	for _, t := range r.Trails {
		if t.Unknown != "" {
			return nil
		}
		if t.Logging && !t.DeliveryFailing && t.Management != "" {
			live = append(live, t)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		a, b := live[i], live[j]
		if a.Organization != b.Organization {
			return a.Organization
		}
		if a.MultiRegion != b.MultiRegion {
			return a.MultiRegion
		}
		return a.Name < b.Name
	})
	covers := func(t inventory.Trail, class string) bool { return t.Management == "all" || t.Management == class }
	paid := map[string][]string{}  // trail ARN -> the names of the free copies it duplicates
	shown := map[string][]string{} // the same, as the Detail says them
	classes := map[string][]string{}
	for _, class := range []string{"read", "write"} {
		var first *inventory.Trail
		for i := range live {
			if !covers(live[i], class) {
				continue
			}
			if first == nil {
				first = &live[i]
				continue
			}
			if !hasName(paid[live[i].ARN], first.Name) {
				paid[live[i].ARN] = append(paid[live[i].ARN], first.Name)
				label := "trail " + first.Name
				if first.Organization {
					label = "organization trail " + first.Name // a member's own trail may share its name
				}
				shown[live[i].ARN] = append(shown[live[i].ARN], label)
			}
			classes[live[i].ARN] = append(classes[live[i].ARN], class)
		}
	}
	var out []Finding
	for _, t := range live {
		dup := paid[t.ARN]
		if len(dup) == 0 || t.Organization || t.HomeRegion != r.Name {
			continue
		}
		which := strings.Join(classes[t.ARN], " and ")
		where := "in " + r.Name
		if t.MultiRegion {
			where = "in every region where both log"
		}
		out = append(out, Finding{
			Check: "cloudtrail-duplicate", Region: r.Name, Resource: t.Name,
			Detail: fmt.Sprintf("trail %s delivers a second copy of the %s management events %s already delivers %s: AWS bills each copy after the first, $2.00 per 100,000 events (unpriced: costhound can't count the events)",
				t.Name, which, strings.Join(shown[t.ARN], " and "), where),
			Why: "a second copy can be on purpose: a team that can't read the organization trail's S3 bucket (member accounts can't by default), " +
				"or a separate audience such as security or auditors; whether to stop it is an audit and compliance call",
			Fix: []string{
				"# a person's call: check who reads this trail's logs first. Then turn its management events off (keep any data events it logs), or delete it:",
				fmt.Sprintf("# aws cloudtrail delete-trail --region %s --name %s", t.HomeRegion, t.ARN),
			},
			Facts: facts("trail", t.Name, "arn", t.ARN, "home_region", t.HomeRegion, "management", t.Management, "duplicates", strings.Join(dup, " ")),
		})
	}
	return out
}

func hasName(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
