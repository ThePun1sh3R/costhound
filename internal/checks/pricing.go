package checks

// How a check's findings are priced in reports (for costhound checks).
const (
	PriceLiveList = "live list price"
	PriceUnpriced = "unpriced"
	PriceCostOnly = "cost-only"
)

// pricingMode overrides the default (live list price) when a check is
// designed never to price savings, or only to show current cost.
var pricingMode = map[string]string{
	spotCandidateNonprod:      "live Spot price history",
	"old-instance-type":       PriceUnpriced, // move needs testing; never priced
	"lambda-errors":           PriceUnpriced,
	"emr-no-auto-termination": PriceUnpriced,
	"lambda-timeouts":         PriceCostOnly,
	"vpn-tunnels-down":        PriceUnpriced, // kind needs a secret-capable call
	"s3-standard-no-tiering":  PriceCostOnly,
	"s3-versions-no-expiry":   PriceUnpriced, // old-version bytes need S3 Inventory or Storage Lens
	"cloudtrail-duplicate":    PriceUnpriced, // the event count is not readable from the trail
	"nat-no-s3-endpoint":      PriceUnpriced, // NAT data volume is not measured
	"lambda-x86":              PriceUnpriced, // per-function savings need a rebuild
	"s3-abort-mpu":            PriceUnpriced, // stuck MPU bytes are not measured
	"old-cache-nodes":         PriceUnpriced, // node change needs a maintenance window
	"dynamodb-no-autoscaling": PriceCostOnly, // shows provisioned cost, not a saving
	"backup-orphans":          PriceCostOnly, // shows backup cost, not in the waste total
	"custom-metrics":          PriceCostOnly, // tiered monitoring cost, not a saving
	"commitment-underused":    "Cost Explorer (your bill)",
}

// PricingOf is how findings of this check are priced.
func PricingOf(id string) string {
	if p, ok := pricingMode[id]; ok {
		return p
	}
	return PriceLiveList
}
