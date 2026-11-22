package pricing

// Long-tail prices: built-in fallbacks when the Price List doesn't answer
// (us-east-1, read on 2026-11-19). The paid checks' own are in tail_pro.go.
const (
	// OpenSearchNIHHour is OpenSearch Service's standard Extended Support rate
	// per Normalized Instance Hour (us-east-1 and us-west-2; up to $0.0103
	// elsewhere), charged on top of the instances.
	OpenSearchNIHHour = 0.0065
	// Route 53 health checks a month (AmazonRoute53 offer file, 2026-11-18):
	// an account's first 50 on AWS endpoints are free (Health-Check-AWS), then
	// $0.50; $0.75 for an endpoint outside AWS (Health-Check-Non-AWS); each
	// optional feature (HTTPS, string matching, fast interval, latency) $1.00
	// on AWS endpoints (Health-Check-Option-AWS), $2.00 outside.
	R53HealthCheckAWSMonth, R53HealthCheckNonAWSMonth             = 0.50, 0.75
	R53HealthCheckOptionAWSMonth, R53HealthCheckOptionNonAWSMonth = 1.00, 2.00
	// ClientVPNAssociationHour is one Client VPN endpoint subnet association
	// (ClientVPN-EndpointHours); connections ($0.05/h each) come on top.
	ClientVPNAssociationHour = 0.10
	// EKSClusterHour is one EKS control plane in standard support ($0.60/h
	// in extended support; larger provisioned control-plane tiers cost more).
	EKSClusterHour = 0.10
)

// eksTierHour is an EKS control plane by provisioned tier: the tier's own
// rate (us-east-1 list, Price List AmazonEKS ...-ProvisionedTier, 2026-11-19)
// is charged "in addition to the standard Amazon EKS cluster pricing".
var eksTierHour = map[string]float64{"standard": EKSClusterHour, "tier-xl": EKSClusterHour + 1.65, "tier-2xl": EKSClusterHour + 3.40,
	"tier-4xl": EKSClusterHour + 6.90, "tier-8xl": EKSClusterHour + 13.90}

// EKSControlPlaneHour is the control plane's hourly price for a tier; ok is
// false for a tier not in the table.
func EKSControlPlaneHour(tier string) (hourly float64, ok bool) {
	if tier == "" {
		tier = "standard"
	}
	h, ok := eksTierHour[tier]
	return h, ok
}
