package pricing

// GBTier is one volume-priced step for CloudWatch vended logs (begin inclusive,
// end exclusive in binary GiB; EndGB 0 means no upper bound).
type GBTier struct {
	BeginGB float64 `json:"begin_gb"`
	EndGB   float64 `json:"end_gb"` // 0 = +Inf
	USD     float64 `json:"usd_per_gb"`
}

// DefaultVendedLogsIngestTiers is the us-east-1 VendedLog-Bytes ladder
// (verified against the Price List 2026-11-27).
func DefaultVendedLogsIngestTiers() []GBTier {
	return []GBTier{
		{0, VendedLogsTier1GB, 0.50},
		{VendedLogsTier1GB, VendedLogsTier2GB, 0.25},
		{VendedLogsTier2GB, VendedLogsTier3GB, 0.10},
		{VendedLogsTier3GB, 0, 0.05},
	}
}

// DefaultVendedLogsS3EgressTiers is the us-east-1 S3-Egress-Bytes ladder.
func DefaultVendedLogsS3EgressTiers() []GBTier {
	return []GBTier{
		{0, VendedLogsTier1GB, 0.25},
		{VendedLogsTier1GB, VendedLogsTier2GB, 0.15},
		{VendedLogsTier2GB, VendedLogsTier3GB, 0.075},
		{VendedLogsTier3GB, 0, 0.05},
	}
}
