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
	"old-instance-type":      PriceUnpriced, // move needs testing; never priced
	"ec2-graviton":           PriceUnpriced,
	"rds-graviton":           PriceUnpriced,
	"lambda-errors":          PriceUnpriced,
	"vpn-tunnels-down":       PriceUnpriced, // kind needs a secret-capable call
	"s3-standard-no-tiering": PriceCostOnly,
}

// PricingOf is how findings of this check are priced.
func PricingOf(id string) string {
	if p, ok := pricingMode[id]; ok {
		return p
	}
	return PriceLiveList
}
