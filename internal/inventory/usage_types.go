package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

// Usage is what the paid checks add on top of the inventory: how much each
// resource was actually used over the last Days days (CloudWatch sums), and
// the EKS clusters with their support status.
type Usage struct {
	Days             int
	Reservations     []Reservation      // Commitments active, queued, or retired in the last 30 days
	NatBytes         map[string]float64 // NAT gateway ID -> bytes sent out to destinations
	LBTraffic        map[string]float64 // LB ARN -> requests (ALB) or new flows (NLB)
	LogIngest        map[string]float64 // log group name -> bytes ingested
	EndpointBytes    map[string]float64 // interface or Gateway Load Balancer endpoint ID -> bytes processed
	APICacheUse      map[string]float64 // REST API ID/stage -> cache hits + misses over the window
	APIRequests      map[string]float64 // REST API ID/stage -> requests over the window
	Clusters         []EKSCluster
	PublicENIs       []ec2types.NetworkInterface // network interfaces holding a public IPv4
	FlowLogs         map[string][]string         // log group -> the VPC flow logs writing to it ("fl-… (vpc-…)")
	Databases        []Database                  // RDS and Aurora instances with their engine lifecycle
	CostHub          *CostHub                    // account-wide; only on the "global" pseudo-region
	ComputeOptimizer *ComputeOptimizer           // account-wide; only on the "global" pseudo-region (when the hub is off or unread)

	CacheExtendedPrices map[string]ExtendedPrice // ElastiCache node type -> extended-support $/node-hour (Price List)
	TableActivity       map[string]float64       // DynamoDB table -> consumed read+write units over the window (table and indexes)
	EFSConnections      map[string]float64       // EFS file system -> client connections over the window
	StreamRecords       map[string]float64       // Kinesis stream -> records put + read over the window
	CustomMetrics       map[string]int           // CloudWatch namespace (not AWS/*) -> metrics active in the last 3 hours
	CustomMetricsCapped bool                     // listing stopped at the page cap: counts are a floor

	S3Standard            map[string]S3StandardUsage // bucket name -> enough evidence to judge it
	S3StandardPriceSource string

	AI   *AIUsage
	Glue *GlueUsage
	Tail Tail // OpenSearch, SageMaker, Redshift, Transit Gateway (tail.go)
}

type S3StandardUsage struct {
	Bytes      float64
	Tiered     bool
	MonthlyUSD float64 // current storage estimate, zero when regional rates are unavailable
}

// Database is an RDS or Aurora instance and where its engine version stands.
type Database struct {
	ID, Engine, Version, Class, Cluster string
	Major                               string     // matched major version, "" if unknown
	Enrollment                          string     // EngineLifecycleSupport: ...-extended-support or ...-disabled
	StandardEnd                         *time.Time // end of standard support for the major version
	ExtendedEnd                         *time.Time // end of extended support
}

// RDSAPI is the RDS calls the paid checks use.
type RDSAPI interface {
	rds.DescribeDBInstancesAPIClient
	rds.DescribeDBMajorEngineVersionsAPIClient
	rds.DescribeDBEngineVersionsAPIClient
	rds.DescribeBlueGreenDeploymentsAPIClient
}

// Reservation is a commitment the account bought: a Reserved Instance, a
// reserved node or a Savings Plan. End is when its discount stops.
type Reservation struct {
	Kind       string // "EC2", "RDS", "ElastiCache", "OpenSearch", "Redshift", "Savings Plan"
	ID, ARN    string // ARN: "" where the service has none (OpenSearch, Redshift, EC2 in the SDK)
	What       string // instance or node type; a Savings Plan's type
	Product    string // RDS: ProductDescription (engine family); empty elsewhere
	Count      int    // instances or nodes (Savings Plans: 0)
	Commitment string // Savings Plans: $ per hour
	State      string // "active", "retired", or "queued" (a purchase waiting to start: a renewal)
	Start, End time.Time
	// Key is what a renewal must match: EC2 type, scope and platform; a
	// Savings Plan's type, instance family and region. Hourly is a Savings
	// Plan's commitment as a number (0 if unreadable).
	Key    string
	Hourly float64
}

// ReservedInstancesAPI is EC2's Reserved Instance list (one call, no pages).
type ReservedInstancesAPI interface {
	DescribeReservedInstances(context.Context, *ec2.DescribeReservedInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error)
}

// ReservationClients read the commitments the account bought, per region.
type ReservationClients struct {
	EC2         ReservedInstancesAPI
	RDS         rds.DescribeReservedDBInstancesAPIClient
	ElastiCache elasticache.DescribeReservedCacheNodesAPIClient
	OpenSearch  opensearch.DescribeReservedInstancesAPIClient
	Redshift    redshift.DescribeReservedNodesAPIClient
}

// PricingAPI is the AWS Price List call.
type PricingAPI interface {
	pricing.GetProductsAPIClient
}

// ExtendedPrice is the extended-support surcharge per node-hour, by year.
type ExtendedPrice struct{ Year12, Year3 float64 }

// EKSCluster is a cluster and where its Kubernetes version stands.
type EKSCluster struct {
	Name          string
	Version       string
	Created       *time.Time
	VersionStatus string     // standard-support, extended-support, unsupported, or "" if unknown
	EndOfStandard *time.Time // when the version leaves standard support
	EndOfExtended *time.Time // when it leaves extended support (AWS upgrades the cluster then)
	// SupportType is the cluster's upgrade policy: EXTENDED (the default) pays
	// for extended support; STANDARD is upgraded by AWS when standard support
	// ends and never pays it.
	SupportType string
}

// CloudWatchAPI is the CloudWatch call the paid checks use.
type CloudWatchAPI interface {
	cloudwatch.GetMetricDataAPIClient
	cloudwatch.ListMetricsAPIClient
}

// EKSAPI is the EKS calls the paid checks use.
type EKSAPI interface {
	eks.ListClustersAPIClient
	eks.DescribeClusterVersionsAPIClient
	DescribeCluster(context.Context, *eks.DescribeClusterInput, ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}
