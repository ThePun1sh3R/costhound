package checks

import (
	"fmt"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// UnusedHealthChecks finds Route 53 health checks nothing uses: no record set
// routes on them, no calculated health check counts them, no AWS service made
// them, and they aren't ARC routing controls. Priced at the least they cost:
// the first 50 on AWS endpoints are free, and costhound can't tell an AWS
// endpoint from another one, so only optional features count for sure.
func UnusedHealthChecks(r *inventory.Region, o Options) []Finding {
	if r.HealthCheckUsesUnknown {
		return nil // a record set may name any of them
	}
	var out []Finding
	for _, h := range r.HealthChecks {
		if h.Used || h.Managed != "" {
			continue
		}
		target := ""
		if h.Target != "" {
			target = " of " + h.Target
		}
		f := Finding{
			Evidence: []string{"Route 53: no record set in any hosted zone names it, no calculated health check counts it, no AWS service manages it"},
			Check:    "r53-unused-health-check", Region: "global", Resource: h.ID,
			Facts:  facts("health_check", h.ID, "type", h.Type),
			Detail: fmt.Sprintf("%s health check%s that nothing routes on", strings.ReplaceAll(h.Type, "_", " "), target),
		}
		if h.Disabled {
			f.Detail += " (disabled)"
		}
		if usd := float64(h.Options) * pricing.R53HealthCheckOptionAWSMonth; usd > 0 {
			f.MonthlyUSD, f.Priced = usd, true
			f.Detail += fmt.Sprintf(": at least $%.2f a month for its %d optional feature(s) at the AWS-endpoint rate ($%.2f each for an endpoint outside AWS)",
				usd, h.Options, pricing.R53HealthCheckOptionNonAWSMonth)
		}
		f.Detail += fmt.Sprintf("; the check itself is free for an account's first 50 on AWS endpoints, then $%.2f a month, and $%.2f for an endpoint outside AWS",
			pricing.R53HealthCheckAWSMonth, pricing.R53HealthCheckNonAWSMonth)
		switch {
		case h.Watched:
			f.Confidence, f.Why = "low", "a CloudWatch alarm watches it: it may be kept as a monitor"
		case r.HealthCheckAlarmsUnknown:
			f.Confidence, f.Why = "low", "the CloudWatch alarms couldn't be read: one may watch it"
		case r.TrafficPolicies:
			f.Confidence, f.Why = "low", "the account has traffic policy instances (or they couldn't be listed), which route on health checks named in their policy documents"
		}
		f.Fix = []string{
			"# a health check can be someone's monitor (an alarm, a dashboard, an outside tool): check before deleting",
			"# aws route53 delete-health-check --health-check-id " + h.ID,
		}
		out = append(out, f)
	}
	return out
}
