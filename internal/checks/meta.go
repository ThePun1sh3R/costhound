package checks

import "strings"

// meta is what costhound says about a check's findings by default: how sure
// it is, why, and which read-only AWS calls the finding comes from. Checks
// can override confidence and add evidence per finding.
type meta struct {
	Confidence string // high, medium, low
	Why        string
	Sources    []string
}

// Meta holds one entry per check; a test makes sure none is missing.
var Meta = map[string]meta{
	"unattached-ebs":          {"high", "AWS reports the disk attached to nothing; it's billed in full", []string{"ec2:DescribeVolumes"}},
	"stopped-instance":        {"high", "the instance has been stopped for -stopped-days or longer (7 by default; an unknown stop date counts), and its disks are still billed", []string{"ec2:DescribeInstances", "ec2:DescribeVolumes"}},
	"idle-ip":                 {"high", "the address is attached to nothing or to a stopped instance", []string{"ec2:DescribeAddresses", "ec2:DescribeInstances"}},
	"old-snapshots":           {"medium", "old and not used by any of your AMIs, AWS Backup or DLM; check nothing restores from them by hand", []string{"ec2:DescribeSnapshots", "ec2:DescribeImages", "ec2:DescribeVolumes"}},
	"gp2-to-gp3":              {"high", "a price change only: gp3 is set to match the disk's gp2 speed, applied online", []string{"ec2:DescribeVolumes"}},
	"piops-to-gp3":            {"medium", "gp3 can deliver the provisioned IOPS; keep io1/io2 if the app needs their latency or durability", []string{"ec2:DescribeVolumes"}},
	"idle-memorydb":           {"medium", "no command of any family on any node over the whole window; MemoryDB's own monitoring holds 2 to 4 connections, so a client that only connects or pings looks the same", []string{"memorydb:DescribeClusters", "cloudwatch:ListMetrics", "cloudwatch:GetMetricData"}},
	"idle-emr":                {"medium", "IsIdle was 1 at every 5-minute check: no YARN task or job. Work outside YARN (a Python notebook kernel, a custom daemon) looks the same", []string{"elasticmapreduce:ListClusters", "elasticmapreduce:DescribeCluster", "elasticmapreduce:ListSteps", "cloudwatch:GetMetricData"}},
	"eks-overrequested":       {"low", "pods request most of the nodes but use half of that or less even in the busiest 5 minutes; the saving assumes an autoscaler removes the freed nodes", []string{"eks:ListClusters", "eks:DescribeCluster", "eks:ListNodegroups", "eks:ListFargateProfiles", "eks:ListCapabilities", "ec2:DescribeInstances", "cloudwatch:ListMetrics", "cloudwatch:GetMetricData", "cloudwatch:ContainerInsights"}},
	"idle-attached-ebs":       {"medium", "no read and no write on any day of the window while attached to a running instance; a disk read only at boot, at month end or in a disaster looks the same", []string{"ec2:DescribeVolumes", "ec2:DescribeInstances", "cloudwatch:GetMetricData"}},
	"cloudtrail-duplicate":    {"medium", "two logging trails copy the same management events in a region, and AWS bills every copy after the first; a second copy can be on purpose", []string{"cloudtrail:DescribeTrails", "cloudtrail:GetTrailStatus", "cloudtrail:GetEventSelectors"}},
	"api-cache-unused":        {"high", "the cache is provisioned while caching is off for every method and the stage default: billed by the hour for nothing", []string{"apigateway:GetRestApis", "apigateway:GetStages"}},
	"quiet-api-cache":         {"medium", "no hits and no misses in the window; a stage called only now and then looks the same", []string{"apigateway:GetRestApis", "apigateway:GetStages", "cloudwatch:GetMetricData"}},
	"io1-to-io2":              {"high", "the same volume class, as fast or faster, at a lower IOPS price, changed online", []string{"ec2:DescribeVolumes"}},
	"log-retention":           {"medium", "never-expiring logs keep growing; how long to keep them is your call", []string{"logs:DescribeLogGroups"}},
	"idle-load-balancer":      {"high", "no registered targets, so it can't forward traffic, and no listener redirects or returns a fixed response", []string{"elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeTargetGroups", "elasticloadbalancing:DescribeTargetHealth"}}, // not in Sources: DescribeListeners/DescribeRules only lower the confidence, never drop a finding, so a failed read mustn't block booking a deleted one
	"nat-no-s3-endpoint":      {"medium", "the VPC has a NAT gateway but no free S3 gateway endpoint; the saving depends on how much S3 traffic it carries", []string{"ec2:DescribeNatGateways", "ec2:DescribeVpcEndpoints"}},
	"paid-s3-endpoint":        {"medium", "a free gateway endpoint covers traffic from inside the VPC; keep this one if outside clients use it", []string{"ec2:DescribeVpcEndpoints"}},
	"old-instance-type":       {"medium", "newer generations are faster and usually cheaper at the same size, but a move needs testing", []string{"ec2:DescribeInstances"}},
	"ec2-graviton":            {"low", "every program on it needs an arm64 build, and a Reserved Instance or Savings Plan for its family shrinks the saving", []string{"ec2:DescribeInstances", "ec2:DescribeInstanceTypeOfferings", "autoscaling:DescribeAutoScalingGroups"}},
	"unused-secret":           {"high", "Secrets Manager's own last-read date is older than -secret-days (90 by default), or the secret is that old and was never read", []string{"secretsmanager:ListSecrets"}},
	"ecr-cleanup":             {"high", "untagged images aren't pulled by tag and pile up without a lifecycle policy", []string{"ecr:DescribeRepositories", "ecr:DescribeImages", "ecr:GetLifecyclePolicy"}},
	"lambda-x86":              {"low", "arm64 is cheaper, but code with native x86 parts must be rebuilt and tested", []string{"lambda:ListFunctions"}},
	"s3-abort-mpu":            {"medium", "no rule cleans up failed uploads; how many bytes are stuck isn't measured here", []string{"s3:ListAllMyBuckets", "s3:GetLifecycleConfiguration"}},
	"old-cache-nodes":         {"medium", "newer node types are cheaper or faster, but a change needs a maintenance window", []string{"elasticache:DescribeCacheClusters"}},
	"dynamodb-no-autoscaling": {"medium", "provisioned with no auto scaling pays for the peak all day; on-demand may cost less or more depending on traffic", []string{"dynamodb:ListTables", "dynamodb:DescribeTable", "application-autoscaling:DescribeScalableTargets"}},
	"fast-snapshot-restore":   {"high", "AWS bills every enabled snapshot per AZ per hour, restore or not", []string{"ec2:DescribeFastSnapshotRestores"}},
	"backup-orphans":          {"low", "the resource is gone, but backups are often kept on purpose after a delete", []string{"backup:ListBackupVaults", "backup:ListRecoveryPointsByBackupVault", "ec2:DescribeVolumes", "ec2:DescribeInstances"}},
	"efs-no-mount-targets":    {"high", "with no mount targets nothing can reach the file system", []string{"elasticfilesystem:DescribeFileSystems"}},
	"unused-ami":              {"medium", "nothing runs from it or launched from it lately, and EC2 knows of nothing that names it; other accounts may still launch it if it's shared", []string{"ec2:DescribeImages", "ec2:DescribeInstances", "ec2:DescribeImageReferences", "ec2:DescribeSnapshots", "autoscaling:DescribeAutoScalingGroups"}},
	"idle-dedicated-host":     {"high", "AWS reports no instances on the host, and a host is billed per hour from allocation to release", []string{"ec2:DescribeHosts"}},
	"disabled-kms-key":        {"low", "AWS calls disabling the safe alternative to deleting, so the key may be kept on purpose; deleting it makes data encrypted with it unreadable", []string{"kms:ListAliases", "kms:ListKeys", "kms:DescribeKey", "kms:ListKeyRotations"}},
	"unused-private-ca":       {"medium", "AWS bills a Private CA in every state but DELETED and FAILED, and this one can't issue; a root kept disabled on purpose, or a subordinate kept until its certificates expire, looks the same", []string{"acm-pca:ListCertificateAuthorities"}}, // the policy, trust-anchor and ACM reads only lower the confidence, so they aren't Sources
	"empty-eks-cluster":       {"high", "no node groups, Fargate profiles, capabilities or Auto Scaling groups, and no instance in its VPC: nothing can run on it", []string{"eks:ListClusters", "eks:DescribeCluster", "eks:ListNodegroups", "eks:ListFargateProfiles", "eks:ListCapabilities", "autoscaling:DescribeAutoScalingGroups", "ec2:DescribeInstances"}},
	"nat-no-routes":           {"high", "no route targets it and no appliance is attached, so nothing can send traffic through it; a standby NAT for a failover or a staged cutover looks the same", []string{"ec2:DescribeNatGateways", "ec2:DescribeRouteTables"}},
	"rds-orphan-snapshot":     {"medium", "the database is gone and its snapshots are older than the threshold; final snapshots are often kept on purpose", []string{"rds:DescribeDBSnapshots", "rds:DescribeDBClusterSnapshots", "rds:DescribeDBInstances", "rds:DescribeDBClusters"}},

	"idle-nat":                     {"high", "CloudWatch shows (almost) no bytes sent out over the whole window", []string{"ec2:DescribeNatGateways", "cloudwatch:GetMetricData"}},
	"quiet-load-balancer":          {"high", "CloudWatch shows zero requests or new connections over the whole window", []string{"elasticloadbalancing:DescribeLoadBalancers", "cloudwatch:GetMetricData"}},
	"idle-endpoint":                {"high", "PrivateLink reports no bytes processed over the whole window", []string{"ec2:DescribeVpcEndpoints", "cloudwatch:GetMetricData"}},
	"log-ingest":                   {"medium", "the saving assumes the logs can live in the Essentials class, formerly Infrequent Access (no metric filters, subscription filters or Live Tail)", []string{"logs:DescribeLogGroups", "cloudwatch:GetMetricData"}},
	"public-ipv4-in-use":           {"medium", "every public IPv4 is billed; whether these need one is your call", []string{"ec2:DescribeNetworkInterfaces"}},
	"eks-extended-support":         {"high", "AWS's own version table says this Kubernetes version is in extended support", []string{"eks:ListClusters", "eks:DescribeCluster", "eks:DescribeClusterVersions"}},
	"rds-extended-support":         {"high", "AWS's own lifecycle dates for this engine version; priced per vCPU-hour", []string{"rds:DescribeDBInstances", "rds:DescribeDBMajorEngineVersions"}},
	"elasticache-extended-support": {"high", "AWS's published schedule, priced with the surcharge from the AWS Price List", []string{"elasticache:DescribeCacheClusters", "pricing:GetProducts"}},
	"idle-dynamodb":                {"high", "CloudWatch shows zero reads and writes on the table and its indexes over the whole window", []string{"dynamodb:DescribeTable", "cloudwatch:GetMetricData"}},
	"idle-dynamodb-index":          {"medium", "no reads in 90 days, while the table's writes are still copied into it; a job that runs less often than that, or one about to start, looks the same", []string{"dynamodb:ListTables", "dynamodb:DescribeTable", "cloudwatch:GetMetricData"}},
	"dynamodb-table-class":         {"medium", "modelled from the window's CloudWatch use and the region's prices, not the bill: a change in traffic changes the answer", []string{"dynamodb:ListTables", "dynamodb:DescribeTable", "cloudwatch:GetMetricData", "pricing:GetProducts"}},
	"idle-accelerator":             {"medium", "it routes nothing, but it may be kept to hold its static IP addresses, which deleting releases for good", []string{"globalaccelerator:ListAccelerators", "globalaccelerator:ListListeners", "globalaccelerator:ListEndpointGroups"}},
	"quiet-accelerator":            {"medium", "no new flow and no byte over the window while its endpoints kept reporting health; a standby, or one kept for its static IP addresses, looks the same", []string{"globalaccelerator:ListAccelerators", "globalaccelerator:ListListeners", "globalaccelerator:ListEndpointGroups", "cloudwatch:GetMetricData (us-west-2)"}},
	"lambda-errors":                {"medium", "CloudWatch's error count over the window; a function can fail on purpose (a queue consumer retrying a batch, a Step Functions task), and fixing the bug lowers the bill only when the failures were retries", []string{"lambda:ListFunctions", "cloudwatch:GetMetricData"}},
	"idle-provisioned-concurrency": {"medium", "no calls used it, or its peak plus a quarter is below what it keeps warm, over the whole window; traffic that comes rarely but must never wait looks the same", []string{"lambda:ListFunctions", "lambda:ListProvisionedConcurrencyConfigs", "lambda:GetProvisionedConcurrencyConfig", "application-autoscaling:DescribeScalableTargets", "cloudwatch:GetMetricData", "cloudwatch:ListMetrics"}},
	"aurora-serverless-pause":      {"medium", "no connection, connection attempt (Aurora MySQL) or IAM sign-in in the idle hours; on Aurora PostgreSQL a connection shorter than a minute doesn't show, and the first connection after a pause waits about 15 seconds", []string{"rds:DescribeDBClusters", "rds:DescribeDBInstances", "rds:DescribeDBEngineVersions", "cloudwatch:GetMetricData"}},
	"fargate-oversized":            {"medium", "the busiest task's 20-second peaks over the window, with headroom; a spike shorter than that, or a job outside the window, isn't seen", []string{"ecs:ListClusters", "ecs:ListServices", "ecs:DescribeServices", "ecs:DescribeTaskDefinition", "application-autoscaling:DescribeScalableTargets", "cloudwatch:GetMetricData", "pricing:GetProducts"}},
	"custom-metrics":               {"low", "a count of what's billed; most of it is probably in use", []string{"cloudwatch:ListMetrics"}},
	"idle-efs":                     {"high", "CloudWatch shows no client connections over the whole window", []string{"elasticfilesystem:DescribeFileSystems", "cloudwatch:GetMetricData"}},
	"idle-kinesis":                 {"high", "CloudWatch shows no records written or read over the whole window", []string{"kinesis:ListStreams", "kinesis:DescribeStreamSummary", "kinesis:DescribeAccountSettings", "cloudwatch:GetMetricData"}},
	"opensearch-extended-support":  {"high", "AWS's published OpenSearch schedule, priced from the Price List (in the extension window the surcharge equals the instance price)", []string{"es:ListDomainNames", "es:DescribeDomains", "pricing:GetProducts"}},
	"idle-opensearch":              {"medium", "CloudWatch shows no requests, searches or indexing while the domain kept reporting; OpenSearch gives no creation date, so a brand-new domain can look idle", []string{"es:ListDomainNames", "es:DescribeDomains", "cloudwatch:GetMetricData"}},
	"idle-dms-instance":            {"low", "an instance at least 7 days old with no tasks, or only tasks stopped or failed for at least 7 days (Pro also needs its CPU to peak at 5% or less over the window, and is then medium); a migration waiting for its cutover looks the same", []string{"dms:DescribeReplicationInstances", "dms:DescribeReplicationTasks", "dms:DescribeReplicationConfigs", "dms:DescribeReplications", "cloudwatch:GetMetricData", "pricing:GetProducts", "savingsplans:DescribeSavingsPlans"}},
	"unused-workspace":             {"medium", "nobody has signed in for 30 days or more by WorkSpaces' own record; a seat kept for a returning user or for emergencies, or one used only over RDP or SSH, which WorkSpaces may not record, looks the same", []string{"workspaces:DescribeWorkspaces", "workspaces:DescribeWorkspacesConnectionStatus"}}, // bundle and image reads only change the price
	"idle-transfer-server":         {"medium", "AWS bills a server's enabled protocols whether it's stopped or online; a server stopped between scheduled transfers, or an online one kept for a quarterly or yearly exchange or for disaster recovery, looks the same", []string{"transfer:ListServers", "transfer:DescribeServer", "transfer:ListUsers", "cloudwatch:GetMetricData", "pricing:GetProducts"}},
	"idle-resolver-endpoint":       {"medium", "no DNS query in at least 30 days and today so far, while its health metric reported every day; outbound counts queries through rules shared with other accounts; an endpoint kept for failover or a rare job looks the same", []string{"route53resolver:ListResolverEndpoints", "cloudwatch:GetMetricData", "pricing:GetProducts"}},
	"idle-fsx":                     {"medium", "no client reads or writes on any full day of at least 30, while a capacity metric (for ONTAP, the client metrics themselves) reported every day; high only when ClientConnections was also 0 every day (ONTAP doesn't publish it, and Windows publishes it only at 32 MBps of throughput or more); a share kept for disaster recovery, an archive read once a quarter or a drive people mapped and never open looks the same", []string{"fsx:DescribeFileSystems", "fsx:DescribeVolumes", "cloudwatch:GetMetricData", "cloudwatch:ListMetrics", "pricing:GetProducts"}}, // not in Sources: DescribeDataRepositoryAssociations only changes the Fix/Detail backup note, never drops a finding, so a failed read mustn't block booking a deleted one
	"idle-opensearch-serverless":   {"medium", "no request (2xx to 5xx) to its collections, and for NextGen no growth in its hot storage, over the whole window while ActiveCollection reported on every day but one (90 days before a delete is offered); an empty NextGen group bills its minimum; a monthly job, or a knowledge base queried rarely, looks the same over a short window", []string{"aoss:ListCollections", "aoss:BatchGetCollection", "aoss:ListCollectionGroups", "aoss:BatchGetCollectionGroup", "cloudwatch:GetMetricData", "cloudwatch:ListMetrics", "bedrock:ListKnowledgeBases", "bedrock:GetKnowledgeBase"}},
	"idle-sagemaker-endpoint":      {"high", "CloudWatch shows zero invocations on every variant while its instances kept reporting", []string{"sagemaker:ListEndpoints", "sagemaker:DescribeEndpoint", "sagemaker:DescribeEndpointConfig", "cloudwatch:GetMetricData"}},
	"idle-redshift":                {"high", "CloudWatch shows no database connections in any sample over the whole window", []string{"redshift:DescribeClusters", "cloudwatch:GetMetricData"}},
	"idle-tgw-attachment":          {"medium", "CloudWatch shows no bytes in or out over the whole window; an attachment may still be kept for a planned or standby route", []string{"ec2:DescribeTransitGatewayAttachments", "cloudwatch:GetMetricData"}},
	"idle-elasticache":             {"high", "CloudWatch shows no read or write commands on any node while every node kept reporting", []string{"elasticache:DescribeCacheClusters", "cloudwatch:GetMetricData"}},
	"idle-msk":                     {"medium", "CloudWatch shows no messages or bytes in or out on any broker while every broker kept reporting; its topics may still hold data someone needs", []string{"kafka:ListClusters", "kafka:ListNodes", "cloudwatch:GetMetricData"}},
	"idle-client-vpn":              {"medium", "no connection in any CloudWatch sample and no bytes in or out over the whole window; an endpoint kept for emergencies can sit unused for months: judge it with -usage-days 90", []string{"ec2:DescribeClientVpnEndpoints", "ec2:DescribeClientVpnTargetNetworks", "cloudwatch:GetMetricData"}},
	"vpn-tunnels-down":             {"medium", "every tunnel was down in every CloudWatch sample over the whole window; a DR link that only comes up on failover, or a tunnel the other side starts only when it needs it, looks the same", []string{"cloudwatch:ListMetrics", "cloudwatch:GetMetricData"}},
	"idle-classic-lb":              {"high", "no instance in service, so it can't serve anything", []string{"elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeInstanceHealth"}},
	"rds-old-class":                {"medium", "AWS lists the class as previous generation; a current class is newer hardware, usually at the same price or less; the change restarts the database and needs a test", []string{"rds:DescribeDBInstances"}},
	"s3-versions-no-expiry":        {"medium", "no rule expires its old versions; they may be kept as a backup on purpose, and their size isn't readable without S3 Inventory or a Storage Lens export", []string{"s3:ListAllMyBuckets", "s3:GetBucketVersioning", "s3:GetLifecycleConfiguration"}},
	"r53-unused-health-check":      {"medium", "no record set, calculated health check or AWS service uses it; one kept as a monitor for an outside tool looks the same", []string{"route53:ListHealthChecks", "route53:ListHostedZones", "route53:ListResourceRecordSets", "cloudwatch:DescribeAlarms"}},
	"empty-hosted-zone":            {"medium", "only its NS and SOA records, so it answers nothing; a registrar may still delegate to it, and a new zone waiting for records looks the same", []string{"route53:ListHostedZones", "route53:GetDNSSEC"}},
	"orphan-alarm":                 {"medium", "no data for 30 days, and the resource it watches isn't in the region's complete list of its type; a resource that comes back under the same name would bring it back to life", []string{"cloudwatch:DescribeAlarms"}},
	"unused-capacity-reservation":  {"medium", "AWS bills reserved capacity whether instances use it or not; a reservation kept for disaster recovery or a launch to come looks the same", []string{"ec2:DescribeCapacityReservations", "cloudwatch:GetMetricData"}},
	"ec2-detailed-monitoring":      {"medium", "detailed monitoring is billed as about 7 custom metrics an instance; a dashboard, an alarm or faster Auto Scaling may read them", []string{"ec2:DescribeInstances", "autoscaling:DescribeAutoScalingGroups", "ec2:DescribeFleets", "ec2:DescribeSpotFleetRequests", "batch:DescribeComputeEnvironments", "eks:ListClusters", "cloudwatch:DescribeAlarms"}},
	"off-hours":                    {"medium", "no connection, connection attempt or IAM sign-in in the same hours of every week read; a connection shorter than a minute, or one inside the database, looks the same", []string{"rds:DescribeDBInstances", "redshift:DescribeClusters", "cloudwatch:GetMetricData"}},
	"rds-graviton":                 {"medium", "RDS runs the same engine version on the Graviton class; the change restarts the database and should be tested first", []string{"rds:DescribeDBInstances"}},
	"idle-rds":                     {"medium", "no connection in any one-minute sample, no connection attempt (MySQL) and no IAM sign-in over the whole window while it ran; jobs scheduled inside the database (pg_cron, MySQL events, DBMS_SCHEDULER, SQL Server Agent), replication into it, a rare job or a restore drill look the same: check its logs or Performance Insights first", []string{"rds:DescribeDBInstances", "cloudwatch:GetMetricData"}},
	"idle-db-cluster":              {"medium", "no client connection, connection attempt (MySQL), IAM sign-in or Neptune request on any instance of the Aurora, Multi-AZ DB, DocumentDB or Neptune cluster over the whole window, while CPUUtilization reported every day; a rare job, a fallback, a restore drill or replication into it looks the same", []string{"rds:DescribeDBClusters", "rds:DescribeDBInstances", "rds:DescribeBlueGreenDeployments", "cloudwatch:GetMetricData", "pricing:GetProducts"}},
	"quiet-classic-lb":             {"high", "CloudWatch shows no requests and no bytes over the whole window, while its healthy-host count was reported", []string{"elasticloadbalancing:DescribeLoadBalancers", "elasticloadbalancing:DescribeInstanceHealth", "cloudwatch:GetMetricData"}},
	"idle-network-firewall":        {"medium", "no Network Firewall metric in the last 14 days (only nonzero values are published) on a firewall older than that; one kept ready for a cutover or a failover path looks the same", []string{"network-firewall:ListFirewalls", "network-firewall:DescribeFirewall", "ec2:DescribeVpcEndpoints", "cloudwatch:ListMetrics"}},
	"idle-ec2":                     {"medium", "every day of the window under 2% average CPU (no sample above half of one vCPU, at most 10%) and 5 MB of network, while it ran; a bastion, a license server, a DNS or NTP server, a cold standby or a rare job looks the same", []string{"ec2:DescribeInstances", "cloudwatch:GetMetricData"}},
	"cost-hub":                     {"medium", "AWS Cost Optimization Hub's own recommendation and savings estimate", []string{"cost-optimization-hub:ListRecommendations"}},
	"commitments":                  {"medium", "AWS's purchase recommendation; commitments lock in 1 or 3 years", []string{"cost-optimization-hub:ListRecommendations"}},
	"commitments-expiring":         {"high", "the end date comes from AWS; it isn't waste but a jump in next month's bill", []string{"ec2:DescribeReservedInstances", "rds:DescribeReservedDBInstances", "elasticache:DescribeReservedCacheNodes", "es:DescribeReservedInstances", "redshift:DescribeReservedNodes", "savingsplans:DescribeSavingsPlans"}},
}

// withDefaults fills a finding's key and confidence from the check's meta.
func withDefaults(f Finding) Finding {
	derived := f.Key == "" // the finding is about the resource its label starts with
	if f.TagRef == "" {
		if ref, ok := tagRefs[f.Check]; ok {
			f.TagRef = ref(f, derived)
		}
	}
	if f.Key == "" {
		f.Key = f.Resource
		if i := strings.IndexByte(f.Key, ' '); i > 0 {
			f.Key = f.Key[:i] // "vol-0abc (old-data)" -> "vol-0abc": a renamed tag keeps the same ID
		}
	}
	if m, ok := Meta[f.Check]; ok {
		if f.Confidence == "" {
			f.Confidence = m.Confidence
		}
		if f.Why == "" {
			f.Why = m.Why
		}
	}
	return f
}
