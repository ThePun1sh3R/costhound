package checks

import (
	"fmt"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// acceleratorFinding is what both accelerator checks report: the fixed fee,
// counted; its public IPv4 addresses, named but not counted (BYOIP ones are
// free of that charge, and the read can't tell).
func acceleratorFinding(check string, a inventory.Accelerator, state string) Finding {
	res := a.ID
	if a.Name != "" {
		res += " (" + a.Name + ")"
	}
	enabled := "enabled"
	if !a.Enabled {
		enabled = "disabled"
	}
	return Finding{
		Check: check, Region: "global", Resource: res, Confidence: "medium",
		MonthlyUSD: pricing.AcceleratorHour * pricing.HoursPerMonth, Priced: true,
		Detail: fmt.Sprintf("Global Accelerator, %s: %s; billed $%.3f an hour until it's deleted, enabled or not, plus the standard public IPv4 charge for its %d static IPv4 address(es), not counted here",
			enabled, state, pricing.AcceleratorHour, a.IPv4),
		Evidence: []string{fmt.Sprintf("Global Accelerator (us-west-2): status %s, %d listener(s), %d endpoint group(s), %d endpoint(s), created %s",
			a.Status, a.Listeners, a.Groups, a.Endpoints, dateOf(a.Created))},
		Why: "it may be kept to hold its static IP addresses: deleting releases them for good (unless they're your own, BYOIP), so check no allow-list or DNS record names them",
		Fix: []string{
			"# deleting releases its static IP addresses for good (unless they're BYOIP): check allow-lists and DNS records first",
			fmt.Sprintf("# aws globalaccelerator update-accelerator --region us-west-2 --accelerator-arn %s --no-enabled", a.ARN),
			"# then delete its endpoint groups and listeners (delete-endpoint-group, delete-listener)",
			fmt.Sprintf("# aws globalaccelerator delete-accelerator --region us-west-2 --accelerator-arn %s", a.ARN),
		},
		Facts: facts("accelerator", a.Name, "state", enabled, "listeners", itoa(a.Listeners), "endpoints", itoa(a.Endpoints), "ipv4_addresses", itoa(a.IPv4)),
	}
}

// IdleAccelerator finds standard accelerators that route nothing: disabled,
// or with no listener, no endpoint group or no endpoint. The fixed fee runs
// until the accelerator is deleted, enabled or not. One being set up (not
// DEPLOYED, or under a day old) or one whose listeners or endpoint groups
// couldn't be read is left alone; standby endpoints (unhealthy, weight 0, a
// 0% traffic dial) still count as endpoints.
func IdleAccelerator(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, a := range r.Accelerators {
		if a.Unknown || a.Status != "DEPLOYED" || days(o.Now, a.Created) < 1 {
			continue
		}
		var state string
		switch {
		case !a.Enabled:
			state = "disabled, it routes nothing"
		case a.Listeners == 0:
			state = "no listener, so it routes nothing"
		case a.Groups == 0:
			state = "no endpoint group, so it routes nothing"
		case a.Endpoints == 0:
			state = "no endpoint, so it routes nothing"
		default:
			continue
		}
		out = append(out, acceleratorFinding("idle-accelerator", a, state))
	}
	return out
}
