// Package pricing holds the prices costhound puts on each finding: built-in
// us-east-1 on-demand list prices, and (paid plans) a per-region Book loaded
// from the AWS Price List. They are estimates for ranking waste, not a bill.
package pricing

import (
	"strings"
)

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

	// CloudWatch Logs stored bytes (AmazonCloudWatch offer 20261119171040,
	// us-east-1, verified 2026-11-24): TimedStorage-ByteHrs $0.03 Standard;
	// IA-TimedStorage-ByteHrs $0.018 Infrequent Access log class; Archive
	// Instant Access $0.006 where AWS reports that tier.
	LogsStoredGBMonth               = 0.03
	LogsStoredIAGBMonth             = 0.018
	LogsStoredArchiveInstantGBMonth = 0.006
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
	// CloudWatch Logs custom PutLogEvents ingestion (AmazonCloudWatch offer
	// 20261119171040, us-east-1, verified 2026-11-24): DataProcessing-Bytes
	// $0.50/GB flat (Standard); DataProcessingIA-Bytes $0.25/GB.
	LogsIngestStandardGB = 0.50
	LogsIngestIAGB       = 0.25
	// EKS control plane: $0.10/h in standard support, $0.60/h in extended
	// support, so extended support adds $0.50/h per cluster.
	EKSExtendedExtraMonthly = 0.50 * HoursPerMonth
)

// Vended log ingestion to CloudWatch Logs (AmazonCloudWatch offer
// 20261119171040, us-east-1, verified 2026-11-24): USE1-VendedLog-Bytes
// tiers per account-month — 0–10 TiB $0.50/GB, 10–30 TiB $0.25, 30–50 TiB
// $0.10, above $0.05. Custom PutLogEvents stays flat (LogsIngestStandardGB).
const (
	VendedLogsTier1GB = 10 * 1024 // 10 TiB
	VendedLogsTier2GB = 30 * 1024
	VendedLogsTier3GB = 50 * 1024
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

// Batch 3 / EFS prices (AmazonEFS offer 20261104213451, us-east-1, verified
// 2026-11-24). Standard: TimedStorage-ByteHrs $0.30 Regional,
// TimedStorage-Z-ByteHrs $0.16 One Zone. IA: IATimedStorage-ByteHrs $0.025
// Regional Bursting/Provisioned, IATimedStorage-ET-ByteHrs $0.016 Regional
// Elastic, IATimedStorage-Z-ByteHrs $0.0133 One Zone. Archive:
// ArchiveTimedStorage-ByteHrs $0.008. Provisioned throughput:
// ProvisionedTP-MiBpsHrs $6.00/(MiB/s)-month.
const (
	FastSnapshotRestoreHour   = 0.75 // per snapshot per AZ (one DSU)
	EFSStandardGBMonth        = 0.30
	EFSStandardOneZoneGBMonth = 0.16
	EFSIAGBMonth              = 0.025
	EFSIAElasticGBMonth       = 0.016
	EFSIAOneZoneGBMonth       = 0.0133
	EFSArchiveGBMonth         = 0.008
	EFSProvisionedMiBpsMon    = 6.00
	BackupEBSGBMonth          = 0.05 // AWS Backup EBS (warm) storage
)

// LogsStoredRate is the GB-month rate for a CloudWatch Logs class or storage
// tier. standardGB is the Standard rate (regional Book or LogsStoredGBMonth).
// assumedStandard is true when the API did not report a class/tier (only
// storedBytes): callers should say the estimate uses the Standard rate.
func LogsStoredRate(class string, standardGB float64) (rate float64, assumedStandard bool) {
	if standardGB <= 0 {
		standardGB = LogsStoredGBMonth
	}
	switch strings.ToUpper(strings.TrimSpace(class)) {
	case "INFREQUENT_ACCESS":
		return LogsStoredIAGBMonth, false
	case "ARCHIVE_INSTANT_ACCESS":
		return LogsStoredArchiveInstantGBMonth, false
	case "STANDARD", "ESSENTIALS", "DELIVERY":
		return standardGB, false
	case "":
		return standardGB, true
	default:
		return standardGB, true
	}
}

// EFSMonthly prices an EFS file system's storage and provisioned throughput.
// oneZone is true when AvailabilityZoneName is set (One Zone file system).
// throughputMode is bursting, provisioned, or elastic (DescribeFileSystems).
func EFSMonthly(standard, ia, archive int64, provisionedMiBps float64, oneZone bool, throughputMode string) float64 {
	gb := func(b int64) float64 { return float64(b) / (1 << 30) }
	std, iaRate := EFSStandardGBMonth, EFSIAGBMonth
	if oneZone {
		std, iaRate = EFSStandardOneZoneGBMonth, EFSIAOneZoneGBMonth
	} else if strings.EqualFold(throughputMode, "elastic") {
		iaRate = EFSIAElasticGBMonth
	}
	return gb(standard)*std + gb(ia)*iaRate + gb(archive)*EFSArchiveGBMonth + provisionedMiBps*EFSProvisionedMiBpsMon
}
