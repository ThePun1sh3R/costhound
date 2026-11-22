// Package pricing holds the prices costhound puts on each finding: built-in
// us-east-1 on-demand list prices, and (paid plans) a per-region Book loaded
// from the AWS Price List. They are estimates for ranking waste, not a bill.
package pricing

// HoursPerMonth is the hour count AWS uses for monthly estimates.
const HoursPerMonth = 730.0

// Per GiB-month storage prices.
const (
	GP2GBMonth      = 0.10
	GP3GBMonth      = 0.08
	IO1GBMonth      = 0.125
	IO2GBMonth      = 0.125
	ST1GBMonth      = 0.045
	SC1GBMonth      = 0.015
	StandardGBMonth = 0.05

	SnapshotGBMonth        = 0.05
	SnapshotArchiveGBMonth = 0.0125

	LogsStoredGBMonth = 0.03
)

// Provisioned performance prices (per unit-month).
const (
	GP3IOPSMonth       = 0.005 // per IOPS above the free 3,000
	GP3ThroughputMonth = 0.04  // per MiB/s above the free 125
	IO1IOPSMonth       = 0.065
	GP3FreeIOPS        = 3000
	GP3FreeThroughput  = 125
)

// CloudWatch alarms, each a month (AmazonCloudWatch offer, us-east-1,
// 2026-11-19: CW:AlarmMonitorUsage, CW:HighResAlarmMonitorUsage). An
// account's first 10 standard-resolution alarms are free
// (Global-CW:AlarmMonitorUsage 0-10); high-resolution ones never are.
const (
	CWAlarmMonth        = 0.10
	CWHighResAlarmMonth = 0.30
	CWFreeAlarms        = 10
)

// PublicIPv4Monthly is the charge for any public IPv4 address, attached or not
// (AWS has charged $0.005/hour for every public IPv4 since February 2024).
const PublicIPv4Monthly = 0.005 * HoursPerMonth

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Paid-plan prices (us-east-1 list).
const (
	// NAT gateway hourly charge; data processing ($0.045/GB) comes on top.
	NATGatewayMonthly = 0.045 * HoursPerMonth
	// CloudWatch Logs ingestion per GB by log class.
	LogsIngestStandardGB = 0.50
	LogsIngestIAGB       = 0.25
	// EKS control plane: $0.10/h in standard support, $0.60/h in extended
	// support, so extended support adds $0.50/h per cluster.
	EKSExtendedExtraMonthly = 0.50 * HoursPerMonth
)

// More free-plan prices (us-east-1 list).
const (
	SecretMonthly   = 0.40 // Secrets Manager, per secret
	ECRStorageGBMon = 0.10 // ECR private repository storage
)

// InterfaceEndpointAZMonthly is one interface VPC endpoint in one AZ
// ($0.01/h); data processed ($0.01/GB) comes on top.
const InterfaceEndpointAZMonthly = 0.01 * HoursPerMonth

// DynamoDB provisioned capacity, Standard table class (us-east-1).
const (
	DynamoDBRCUHour = 0.00013
	DynamoDBWCUHour = 0.00065
)

// DynamoDBProvisionedMonthly is the monthly price of provisioned capacity.
func DynamoDBProvisionedMonthly(rcu, wcu int64) float64 {
	return (float64(rcu)*DynamoDBRCUHour + float64(wcu)*DynamoDBWCUHour) * HoursPerMonth
}

// Batch 3 prices, us-east-1, read from the AWS Price List on 2026-11-19.
const (
	FastSnapshotRestoreHour = 0.75 // per snapshot per AZ (one DSU)
	EFSStandardGBMonth      = 0.30
	EFSIAGBMonth            = 0.025
	EFSArchiveGBMonth       = 0.008
	EFSProvisionedMiBpsMon  = 6.00
	BackupEBSGBMonth        = 0.05 // AWS Backup EBS (warm) storage
)

// EFSMonthly prices an EFS file system's storage and provisioned throughput.
func EFSMonthly(standard, ia, archive int64, provisionedMiBps float64) float64 {
	gb := func(b int64) float64 { return float64(b) / (1 << 30) }
	return gb(standard)*EFSStandardGBMonth + gb(ia)*EFSIAGBMonth + gb(archive)*EFSArchiveGBMonth + provisionedMiBps*EFSProvisionedMiBpsMon
}
