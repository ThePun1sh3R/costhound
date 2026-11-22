package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/emr"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

// Tail is what the paid long-tail checks read: OpenSearch domains, SageMaker
// endpoints, Redshift clusters and Transit Gateway attachments, how much each
// was used over the window, and their prices from the AWS Price List.
//
// The usage maps hold a key only when CloudWatch answered for it, and a
// "reporting" set says the resource's always-published metric had data
// points: a series with no data can't tell idle from a wrong dimension, so
// the checks judge only what is reporting.
type Tail struct {
	migrationTail
	EKSFit      map[string]*EKSFit // EKS cluster -> Container Insights requests and use, hour by hour
	Domains     []Domain
	Endpoints   []SageMakerEndpoint
	Warehouses  []Warehouse
	Attachments []Attachment
	Kafka       []KafkaCluster
	ClientVPNs  []ClientVPN
	VPNs        []string // Site-to-Site VPN connections whose TunnelState series is active
	Firewalls   []Firewall

	DomainRequests            map[string]float64 // domain name -> requests + searches + indexing over the window
	Invocations               map[string]float64 // "endpoint/variant" -> invocations over the window
	Connections               map[string]float64 // Redshift cluster -> sum of its DatabaseConnections samples
	AttachmentBytes           map[string]float64 // attachment ID -> bytes in + out
	CacheCommands             map[string]float64 // ElastiCache cluster or replication group -> commands of every family
	CacheItems                map[string]float64 // ... -> CurrItems samples: keys held (a warm standby holds data)
	KafkaTraffic              map[string]float64 // MSK cluster -> messages in + bytes in/out, all brokers
	ClientVPNUse              map[string]float64 // Client VPN endpoint -> connection samples + bytes in and out
	VPNTunnelState            map[string]float64 // Site-to-Site VPN connection -> TunnelState sum (0 = every tunnel down in every sample)
	DBConnections             map[string]float64 // RDS DB instance -> connection samples + connection attempts + IAM auth requests
	ClassicTraffic            map[string]float64 // Classic Load Balancer -> requests + estimated bytes processed
	RDSHour                   map[string]float64 // idle-looking RDS DB instance -> its On-Demand $/hour (Price List)
	FirewallHour              float64            // Network Firewall $ per endpoint-hour (0 = built-in)
	FirewallGB                float64            // Network Firewall $ per GB processed (0 = built-in)
	EC2CPU                    map[string]float64 // EC2 instance -> highest daily average CPU %
	EC2CPUPeak                map[string]float64 // EC2 instance -> highest single CPU sample % (5 minutes, or 1 with detailed monitoring)
	EBSOps                    map[string]float64 // attached data volume -> read + write operations over the window
	Window                    int                // the usage window in days the series were read over
	EMRClusters               []EMRCluster
	EMRIdle                   map[string]float64 // EMR cluster -> IsIdle summed over the window (1 per idle 5-minute check)
	EMRSamples                map[string]float64 // EMR cluster -> IsIdle checks over the window
	EMRAutoIdle               map[string]float64 // EMR cluster -> AutoTerminationIsClusterIdle summed (with an auto-termination policy only)
	EMRAutoSamples            map[string]float64 // EMR cluster -> AutoTerminationIsClusterIdle samples
	EMRHDFS                   map[string]float64 // EMR cluster -> highest daily HDFSUtilization %
	EMRFee                    map[string]float64 // instance type -> EMR fee per instance-hour
	MemDBClusters             []MemDBCluster
	MemDBNodeDims             map[string][][]string // MemoryDB cluster -> each node's metric dimensions, as CloudWatch lists them
	MemDBCommands             map[string]float64    // MemoryDB cluster -> commands of every family, on every node, over the window
	MemDBConns                map[string]float64    // MemoryDB cluster -> highest CurrConnections on any node, any day
	MemDBHour                 map[string]float64    // MemoryDB cluster -> On-Demand price per node-hour
	EC2NetIn                  map[string]float64    // EC2 instance -> highest daily NetworkIn bytes
	EC2NetOut                 map[string]float64    // EC2 instance -> highest daily NetworkOut bytes
	EC2Hour                   map[string]float64    // idle-looking EC2 instance -> its On-Demand $/hour for its billing code (Price List)
	EC2TypeHour               map[string]float64    // ec2-graviton, old-instance-type: instance type -> the region's Linux On-Demand $/hour (Price List)
	DDBReads, DDBWrites       map[string]float64    // dynamodb-table-class: on-demand table -> read / write units consumed over the window (with its global secondary indexes)
	DDBProvRead, DDBProvWrite map[string]float64    // auto-scaled provisioned table -> its mean provisioned RCU / WCU over the window (with its indexes)
	DDBPrice                  map[string]float64    // DynamoDB usage type without the region's prefix -> $ per unit (Price List)
	ddbProvRaw                map[string]float64    // "table|metric|series" -> the sum of its daily averages
	GAFlows, GABytes          map[string]float64    // accelerator ID -> new flows / bytes in and out over the window (us-west-2)
	// lambda-errors, per function name, over the window (AWS/Lambda): errors,
	// invocations, the duration's sum (ms) and highest call (ms), invocations
	// on provisioned concurrency, asynchronous events, and errors in the last
	// 3 days. A missing key is a series that wasn't read.
	LambdaErrors, LambdaInvocations, LambdaDurationSum, LambdaDurationMax map[string]float64
	LambdaPCInvocations, LambdaAsync, LambdaRecentErrors                  map[string]float64
	LambdaSources                                                         map[string]LambdaSource // function ARN -> what triggers it
	LambdaSourcesUnread                                                   bool                    // lambda:ListEventSourceMappings failed: the triggers are unknown
	PCConfigs                                                             []PCConfig              // provisioned concurrency configs (nil: not read)
	PCPartial                                                             bool                    // found without ListFunctions: idle ones may be missing
	PCPeak, PCSpill                                                       map[string]float64      // qualified ARN -> busiest ProvisionedConcurrentExecutions; spillover calls
	PCFnInvocations                                                       map[string]float64      // function -> calls on provisioned concurrency, any version or alias
	PCPrice                                                               map[string]float64      // architecture (x86_64, arm64) -> the region's provisioned concurrency $/GB-second (Price List)
	PCSeries                                                              map[string][]string     // function -> the Resource of each ProvisionedConcurrentExecutions series CloudWatch lists (2 weeks); nil: unread
	OffHours                                                              map[string]*Hourly      // Team plans: "rds:<id>" or "redshift:<id>" -> its hours; nil = not read
	AuroraHours                                                           map[string]*Hourly      // Aurora Serverless v2 instance -> its last OffHoursWeeks weeks by the hour; nil = not read
	AuroraBlocked                                                         map[string]string       // Aurora cluster -> the replication that keeps its writer awake ("binary logging", "logical replication"); read when AuroraHours is
	AuroraZeroMin                                                         map[string]bool         // "engine|version" -> whether it can scale to 0 ACUs (DescribeDBEngineVersions); missing = unknown
	ACUHour                                                               map[string]float64      // "engine|standard" or "engine|io-optimized" -> the region's Aurora Serverless v2 price an ACU-hour
	AOSS                                                                  *AOSS                   // OpenSearch Serverless (idle-opensearch-serverless); nil = not read
	CRUsedMax                                                             map[string]float64      // Capacity Reservation -> its busiest daily UsedInstanceCount over the window
	CRHour                                                                map[string]float64      // Capacity Reservation -> the Price List's hourly price of its unused capacity (platform, tenancy)
	IndexReads                                                            map[string]float64      // "table/index" -> ConsumedReadCapacityUnits over IndexWindowDays (nil: not read)
	IndexWrites                                                           map[string]float64      // "table/index" -> ConsumedWriteCapacityUnits over the same days
	IndexSince                                                            map[string]bool         // "table/index" -> written in the window's first week: it existed all along
	TableReports                                                          map[string]bool         // table -> its own read series reported every day but one
	// fargate-oversized: the services and their task sizes (by task
	// definition ARN), the busiest task's peak CPU and memory over the window
	// (percent of the reservation, by service ARN), and the region's Fargate
	// prices by usage type (no region prefix).
	ECSServices          []ECSService
	ECSTaskSizes         map[string]ECSTaskSize
	ECSCPUMax, ECSMemMax map[string]float64
	FargatePrice         map[string]float64
	Reporting            map[string]bool // "domain:x", "variant:e/v", "warehouse:x", "attachment:x", "cache:x", "kafka:x"

	SearchHour      map[string]float64 // OpenSearch instance type -> $/h
	SearchExtraHour map[string]float64 // instance type -> extended-support $/h where it equals the instance price
	SearchGBMonth   map[string]float64 // OpenSearch EBS volume type (gp2, gp3, io1) -> $/GB-month
	SearchNIHHour   float64            // extended support $ per normalized instance hour (0 = built-in)
	SageMakerHour   map[string]float64 // ml.* instance type -> real-time hosting $/h
	RedshiftHour    map[string]float64 // node type -> $/node-hour
	AttachmentHour  float64            // Transit Gateway attachment $/h (0 = built-in)
	CacheNodeHour   map[string]float64 // "engine/cache.m5.large" -> ElastiCache node $/h
	KafkaBrokerHour map[string]float64 // "kafka.m5.large" -> MSK broker $/h
	KafkaGBMonth    float64            // MSK broker storage $/GB-month
	ClientVPNHour   float64            // Client VPN $ per subnet-association hour (0 = built-in)
}

// Domain is an OpenSearch Service domain and its node groups.
type Domain struct {
	Name, ARN, Account string
	Engine, Version    string // "OpenSearch" or "Elasticsearch", and "2.11"
	Nodes              []NodeGroup
	VolumeType         string
	VolumeGiB          int32 // per data node
}

// NodeGroup is one kind of node in a domain.
type NodeGroup struct {
	Role, Type string // Role: data, master or warm
	Count      int
}

// SageMakerEndpoint is a real-time endpoint with instance-backed variants.
type SageMakerEndpoint struct {
	Name     string
	Config   string // the endpoint config it runs now
	Variants []Variant
	Created  *time.Time
}

// Variant is one production variant and the instances it runs now.
type Variant struct {
	Name, Type string
	Count      int
}

// Warehouse is a provisioned Redshift cluster.
type Warehouse struct {
	ID, NodeType, Status string
	Nodes                int
	Created              *time.Time
	Pausable             bool // RA3 or DC2, in a VPC, no HSM, automated snapshots on: what pause-cluster needs
}

// IndexWindowDays is how far back idle-dynamodb-index reads, whatever
// -usage-days says: a monthly or quarterly job may be an index's only reader.
const IndexWindowDays = 90

// OffHoursWeeks is how many whole weeks an Hourly holds, whatever
// -usage-days says.
const OffHoursWeeks = 4

// Hourly is a database's last OffHoursWeeks whole weeks hour by hour, up to
// the last full hour (Team plans: off-hours). An hour with no datapoint
// wasn't running.
type Hourly struct {
	Start    time.Time       // the first hour
	Conns    map[int]float64 // hour index -> the busiest DatabaseConnections sample
	Activity map[int]float64 // hour index -> ConnectionAttempts + IamDbAuthConnectionRequests
	ACU      map[int]float64 // Aurora Serverless v2: hour index -> the average ServerlessDatabaseCapacity
	ACUMin   map[int]float64 // Aurora Serverless v2: hour index -> the lowest; 0 is a pause
}

// AOSS is a region's OpenSearch Serverless collection groups and
// collections (idle-opensearch-serverless).
type AOSS struct {
	Groups      []AOSSGroup
	GroupsRead  bool // ListCollectionGroups answered
	Collections []AOSSCollection
	// End is the end of the last whole day read (UTC midnight; zero = not
	// read), Window the days of OCUs read, AccountOCU the account-level
	// IndexingOCU and SearchOCU by day.
	End        time.Time
	Window     int
	AccountOCU map[int64]OCUDay
	AccountNow []float64 // the account level's latest one-minute indexing and search OCUs; nil = not read
	KBRead     bool      // every Bedrock knowledge base was read
	// The region's prices (0 = not found).
	IndexingOCUHour, SearchOCUHour, S3GBMonth, HotGBMonth float64
}

// OCUDay is one day's indexing and search OCUs: the average and the lowest
// one-minute value (-1 = no datapoint).
type OCUDay struct{ Indexing, Search, IndexingLow, SearchLow float64 }

// AOSSGroup is an OpenSearch Serverless collection group: its generation,
// collection count (-1 = not given), capacity limits and current capacity
// in OCUs (-1 = not set or not read), and its OCUs by day.
type AOSSGroup struct {
	ID, Name, Generation, Standby                  string
	Created                                        time.Time
	Collections                                    int
	MinIndexing, MaxIndexing, MinSearch, MaxSearch float64
	NowIndexing, NowSearch                         float64
	Days                                           map[int64]OCUDay
}

// AOSSCollection is an OpenSearch Serverless collection: Group is its
// collection group's name ("" outside groups), KMS its key's ARN. Requests
// are its requests by day and Active the days ActiveCollection reported
// (nil = not read), Storage its bytes stored (-1 = not read), Wrote the
// last day (Unix time) its stored bytes rose (0 = none, -1 = not read),
// KBIDs and KBNames the Bedrock knowledge bases that keep their vectors in
// it.
type AOSSCollection struct {
	ID, Name, ARN, Type, Status, Group, KMS, Standby string
	Protected                                        bool // deletion protection on
	Created                                          time.Time
	Requests                                         map[int64]float64
	Active                                           map[int64]bool
	Storage                                          float64
	Wrote                                            int64
	KBIDs, KBNames                                   []string
}

// Attachment is a Transit Gateway attachment billed to this account.
type Attachment struct {
	ID, Gateway, Type, Resource, State string
	Created                            *time.Time
}

// KafkaCluster is a provisioned MSK cluster and its brokers.
type KafkaCluster struct {
	Name, ARN, InstanceType string
	BrokerIDs               []string // "1", "2", ...: the metric dimension
	StorageGiB              int32    // EBS storage per broker
	Tiered                  bool     // tiered storage: old data sits in AWS-managed storage, and goes with the cluster
	Created                 *time.Time
}

// ClientVPN is a Client VPN endpoint and its subnet associations, each billed by the hour.
type ClientVPN struct {
	ID           string
	Associations []string // association IDs
	Created      *time.Time
}

// Firewall is a Network Firewall firewall: each endpoint bills by the hour.
type Firewall struct {
	Name, ARN, VPC   string
	Endpoints        []string   // its READY or SCALING endpoints (vpce IDs), one per Availability Zone
	Oldest           *time.Time // creation time of its oldest endpoint (EC2 DescribeVpcEndpoints): it is at least this old
	DeleteProtection bool
	Unjudged         string // why it can't be judged from here ("" if it can)
	Listed, Active   bool   // ListMetrics answered; it listed a series for the firewall
}

// NetworkFirewallAPI is the Network Firewall calls the paid checks use.
type NetworkFirewallAPI interface {
	networkfirewall.ListFirewallsAPIClient
	DescribeFirewall(context.Context, *networkfirewall.DescribeFirewallInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeFirewallOutput, error)
}

// ClientVPNAPI is the EC2 calls for Client VPN endpoints.
type ClientVPNAPI interface {
	ec2.DescribeClientVpnEndpointsAPIClient
	ec2.DescribeClientVpnTargetNetworksAPIClient
}

// KafkaAPI is the MSK calls the paid checks use.
type KafkaAPI interface {
	kafka.ListClustersAPIClient
	kafka.ListNodesAPIClient
}

// AOSSAPI is the OpenSearch Serverless reads idle-opensearch-serverless uses.
type AOSSAPI interface {
	opensearchserverless.ListCollectionsAPIClient
	opensearchserverless.ListCollectionGroupsAPIClient
	BatchGetCollection(context.Context, *opensearchserverless.BatchGetCollectionInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetCollectionOutput, error)
	BatchGetCollectionGroup(context.Context, *opensearchserverless.BatchGetCollectionGroupInput, ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetCollectionGroupOutput, error)
}

// BedrockAgentAPI is the Bedrock reads that name the knowledge base storing
// its vectors in a collection.
type BedrockAgentAPI interface {
	bedrockagent.ListKnowledgeBasesAPIClient
	GetKnowledgeBase(context.Context, *bedrockagent.GetKnowledgeBaseInput, ...func(*bedrockagent.Options)) (*bedrockagent.GetKnowledgeBaseOutput, error)
}

// OpenSearchAPI is the OpenSearch calls the paid checks use.
type OpenSearchAPI interface {
	ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
	DescribeDomains(context.Context, *opensearch.DescribeDomainsInput, ...func(*opensearch.Options)) (*opensearch.DescribeDomainsOutput, error)
}

// SageMakerAPI is the SageMaker calls the paid checks use.
type SageMakerAPI interface {
	sagemaker.ListEndpointsAPIClient
	DescribeEndpoint(context.Context, *sagemaker.DescribeEndpointInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointOutput, error)
	DescribeEndpointConfig(context.Context, *sagemaker.DescribeEndpointConfigInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointConfigOutput, error)
}

// RedshiftAPI is the Redshift call the paid checks use.
type RedshiftAPI interface {
	redshift.DescribeClustersAPIClient
}

// EBSCandidate is an attached data volume idle-attached-ebs judges by its I/O.
type EBSCandidate struct {
	Volume   ec2types.Volume
	Instance ec2types.Instance
	Device   string
}

// EMRAPI reads EMR clusters. Only what EMRCluster holds is kept: a cluster's
// configurations, Kerberos settings, application arguments and step
// arguments can hold passwords and are never copied.
type EMRAPI interface {
	emr.ListClustersAPIClient
	DescribeCluster(context.Context, *emr.DescribeClusterInput, ...func(*emr.Options)) (*emr.DescribeClusterOutput, error)
	ListSteps(context.Context, *emr.ListStepsInput, ...func(*emr.Options)) (*emr.ListStepsOutput, error)
}

// EMRCluster is what costhound keeps of a running or waiting EMR cluster.
type EMRCluster struct {
	ID, Name, State string
	Created         *time.Time
	Apps            []string   // application names only
	ActiveSteps     int        // pending, running or cancel-pending steps: a count only
	LastStep        *time.Time // when the latest step started or ended, any state: its time only
	StepsUnknown    bool
}

// MemoryDBAPI reads MemoryDB clusters (with their shards and nodes).
type MemoryDBAPI interface {
	DescribeClusters(context.Context, *memorydb.DescribeClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeClustersOutput, error)
}

// MemDBCluster is a MemoryDB cluster and its nodes.
type MemDBCluster struct {
	Name, ARN, NodeType, Engine, Status string
	MultiRegion                         string // the Multi-Region cluster it's a member of, if any
	Nodes                               []string
	Created                             *time.Time // its oldest node's
}

// EKSFit is an EKS cluster's Container Insights fill over the usage window,
// 5 minutes at a time: how much of its nodes' capacity pods requested
// (reserved) and used, in percent of the nodes' total capacity (each node's
// series weighed by its capacity; the [ClusterName] series is a plain mean of
// the nodes). The four series hold the same periods; a cluster gets a fit
// only when they cover at least EKSFitCoverage of the window (the reporting
// proof).
type EKSFit struct {
	CPUReserved, MemReserved, CPUUsed, MemUsed []float64
	NodesNow                                   float64 // cluster_node_count in its latest period (0: none)
	CPUMilli                                   float64 // the nodes' CPU capacity, millicores, averaged over the periods
	// Namespaces hold the CPU requests, the largest share first, from the
	// enhanced pod metrics (none without them). NamespacesWhy says why
	// there are none when it isn't that.
	Namespaces       []NamespaceFit
	NamespacesWhy    string
	NamespacesCapped bool // the shares are of the first eksWorkloadCap workloads, not all
}

// NamespaceFit is one namespace's part of a cluster's CPU requests over the
// window, and its busiest 5 minutes' use as a share of its own requests.
// Container Insights' [ClusterName, Namespace, PodName] series hold one
// datapoint per pod (checked on a real cluster: a 3-replica workload's Sum
// per minute is the total, its Average one pod's), so a period's Sum over a
// namespace's workloads is its total times the agent's samples per pod: the
// shares and ratios cancel that out.
type NamespaceFit struct {
	Name      string
	Share     float64 // of the cluster's CPU requests, 0..1
	PeakUse   float64 // the busiest period's use / its requests, 0..1 and more
	Workloads int
}

// LambdaSource is what an event source mapping says about a function: the
// service it reads (sqs, kinesis, dynamodb, kafka...) and whether it reports
// batch item failures. Nothing else of the mapping is kept.
type LambdaSource struct {
	Service           string
	BatchItemFailures bool
}

// LambdaPaidAPI lists the event source mappings (lambda-errors' Why).
type LambdaPaidAPI interface {
	lambda.ListEventSourceMappingsAPIClient
	lambda.ListProvisionedConcurrencyConfigsAPIClient
	GetProvisionedConcurrencyConfig(context.Context, *lambda.GetProvisionedConcurrencyConfigInput, ...func(*lambda.Options)) (*lambda.GetProvisionedConcurrencyConfigOutput, error)
}

// PCConfig is a version's or alias's provisioned concurrency
// (idle-provisioned-concurrency).
type PCConfig struct {
	Function, Qualifier, ARN string // ARN: the qualified one
	Allocated, Requested     int
	Status                   string // READY, IN_PROGRESS, FAILED
	Modified                 time.Time
	Scaled                   bool // an Application Auto Scaling target manages it (or its targets couldn't be read)
	Min                      int  // that target's MinCapacity; -1: the targets couldn't be read
}

// ECSAPI is what fargate-oversized reads of ECS.
type ECSAPI interface {
	ecs.ListClustersAPIClient
	ecs.ListServicesAPIClient
	DescribeServices(context.Context, *ecs.DescribeServicesInput, ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
	DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
}

// ECSService is what fargate-oversized keeps of an ECS service: never its
// events, tags, roles, load balancers or network configuration.
type ECSService struct {
	ARN, Name, Cluster, Status string
	LaunchType                 string        // "" when it uses a capacity provider strategy
	Providers                  []ECSProvider // its capacity provider strategy
	Platform                   string        // platformFamily
	Desired, Running           int
	TaskDef                    string     // its task definition's ARN
	Deployments                int        // how many deployments it has now
	PrimarySince               *time.Time // when its PRIMARY deployment was created
	Controller                 string     // ECS, CODE_DEPLOY or EXTERNAL
	Rollout                    string     // the PRIMARY deployment's rolloutState (COMPLETED, IN_PROGRESS, FAILED; "" when AWS doesn't say)
	TaskSets                   bool
	Scaled                     bool // Application Auto Scaling has a scalable target for it
}

// ECSProvider is one item of a capacity provider strategy.
type ECSProvider struct {
	Name         string
	Weight, Base int32
}

// ECSTaskSize is what fargate-oversized keeps of a task definition: its
// task-level size, platform and the two sums its containers need. Never its
// container definitions (their environment and secrets).
type ECSTaskSize struct {
	CPU, MemMiB     int    // task-level CPU units and memory MiB
	Arch, OS        string // runtimePlatform ("" = Linux x86_64)
	Fargate         bool   // requiresCompatibilities has FARGATE
	ContainerCPU    int    // the sum of its containers' cpu
	ContainerMemMiB int    // the sum of each container's larger of memory and memoryReservation
}
