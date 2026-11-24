// Package checks turns a region inventory into findings. Every check is a pure
// function over inventory.Region, so it is tested without AWS.
package checks

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// Finding is one thing costing money for nothing (or for less than it could).
type Finding struct {
	ID         string   `json:"id"`                // stable across runs: ch_ + hash of account, check, region, key
	Key        string   `json:"key,omitempty"`     // what the ID is built from (default: the resource's ID)
	Account    string   `json:"account,omitempty"` // set in Organization scans
	Check      string   `json:"check"`
	Region     string   `json:"region"`
	Resource   string   `json:"resource"`
	Detail     string   `json:"detail"`
	MonthlyUSD float64  `json:"monthly_usd"`
	Priced     bool     `json:"priced"`
	Fix        []string `json:"fix,omitempty"`
	Confidence string   `json:"confidence"`         // high, medium or low
	Why        string   `json:"why"`                // why that confidence
	Evidence   []string `json:"evidence,omitempty"` // the facts behind it
	// Facts are the resource's identifiers and the state the scan saw, as
	// plain values (lists are space-separated). Fix scripts are built from
	// them, and they re-check the same values before changing anything.
	// One key has a meaning of its own: "ends" is the YYYY-MM-DD on which
	// the resource stops costing by itself (a reservation's end date). A
	// finding gone on or after it is never a fix (report.Compare), and a
	// ledger entry stops counting there (report.Summarize). Set it only for
	// that.
	Facts map[string]string `json:"facts,omitempty"`
	// Owner is who to ask about it: the value of an owner tag on its
	// resource, or the CloudFormation stack or Kubernetes namespace that made
	// it. OwnerFrom says which ("tag Team"), or "none" when the resource has
	// no owner tag. Both stay empty when costhound couldn't look (a finding
	// about several resources, or tags it couldn't read).
	Owner     string `json:"owner,omitempty"`
	OwnerFrom string `json:"owner_from,omitempty"`
	// Creator is who CloudTrail's event history says created the resource,
	// for a finding with no owner tag when -creators asks (Pro); CreatorFrom
	// says how it's known and never holds a name. Owner and OwnerFrom stay as
	// they are: a creator is a hint, and the unowned totals and CI gates must
	// not move with a flag. Created is when the inventory says the resource
	// was made, for the lookup only.
	Creator     string     `json:"creator,omitempty"`
	CreatorFrom string     `json:"creator_from,omitempty"`
	Created     *time.Time `json:"-"`
	// TagRef names the one resource the finding is about as its ARN does
	// after the account ("ec2:volume/vol-0abc"), for the owner lookup.
	TagRef string `json:"-"`
	// Members are the instances a finding about a group covers (not in the
	// report): Cost Optimization Hub may name any of them.
	Members []string `json:"-"`
	// TFAddress is the Terraform resource that manages it, in the state file
	// TFState (-tf-state, Team): fix it in code, not with the CLI.
	TFAddress string `json:"tf_address,omitempty"`
	TFState   string `json:"tf_state,omitempty"`
}

// facts builds a Facts map from key/value pairs, leaving out empty values.
func facts(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			m[kv[i]] = kv[i+1]
		}
	}
	return m
}

func itoa[T ~int | ~int32 | ~int64](v T) string { return fmt.Sprint(v) }

// Options tune the thresholds.
type Options struct {
	Now             time.Time
	Plan            string  // free, pro or team: which checks run
	SnapshotAgeDays int     // snapshots older than this are candidates
	StoppedDays     int     // instances stopped longer than this are flagged
	MinLogGiB       float64 // never-expiring log groups at least this big are listed one by one
	SecretDays      int     // secrets not read for this many days are flagged

	// Paid checks.
	UsageDays         int     // usage window, and the minimum age of a resource to judge it
	IdleNATGiB        float64 // NAT gateways that sent less than this in the window are idle
	MinIngestGiBMonth float64 // log groups ingesting less than this per month aren't listed
	Creators          bool    // -creators: date the unowned findings for the CloudTrail lookup
}

// DefaultOptions are the thresholds the CLI starts with.
func DefaultOptions(now time.Time) Options {
	return Options{Now: now, Plan: license.PlanFree, SnapshotAgeDays: 90, StoppedDays: 7, MinLogGiB: 1, SecretDays: 90,
		UsageDays: 14, IdleNATGiB: 1, MinIngestGiBMonth: 10}
}

// Check is one rule.
type Check struct {
	ID    string
	Title string
	Run   func(*inventory.Region, Options) []Finding
	Plan  string // the cheapest plan that includes it
}

// All is every check, in report order. Each plan runs its own checks and
// those of every cheaper plan.
var All = []Check{
	{"unattached-ebs", "Unattached EBS volumes", UnattachedVolumes, license.PlanFree},
	{"stopped-instance", "Stopped instances still paying for disks", StoppedInstances, license.PlanFree},
	{"idle-ip", "Public IPs doing nothing", IdleIPs, license.PlanFree},
	{"old-snapshots", "Old snapshots nobody uses", OldSnapshots, license.PlanFree},
	{"gp2-to-gp3", "gp2 volumes that should be gp3", GP2ToGP3, license.PlanFree},
	{"log-retention", "Log groups that never expire", LogRetention, license.PlanFree},
	{"idle-load-balancer", "Load balancers with no targets", IdleLoadBalancers, license.PlanFree},
	{"nat-no-s3-endpoint", "NAT traffic to S3 without the free endpoint", NATWithoutS3Endpoint, license.PlanFree},
	{"old-instance-type", "Old-generation instance types", OldInstanceTypes, license.PlanFree},
	{"ec2-graviton", "EC2 instances that could run on Graviton", EC2Graviton, license.PlanFree},
	{"idle-accelerator", "Global Accelerators that route nothing", IdleAccelerator, license.PlanFree},
	{"piops-to-gp3", "io1/io2 volumes that fit gp3", PIOPSToGP3, license.PlanFree},
	{"io1-to-io2", "Multi-Attach io1 volumes that cost less as io2", IO1ToIO2, license.PlanFree},
	{"unused-secret", "Secrets nobody reads", UnusedSecrets, license.PlanFree},
	{"ecr-cleanup", "Container images piling up in ECR", ECRCleanup, license.PlanFree},
	{"lambda-x86", "Lambda functions on x86 instead of arm64", LambdaOnX86, license.PlanFree},
	{"s3-abort-mpu", "Buckets that keep failed uploads forever", S3NoAbortMPU, license.PlanFree},
	{"paid-s3-endpoint", "Paid S3/DynamoDB endpoints where a free one works", PaidS3Endpoint, license.PlanFree},
	{"old-cache-nodes", "ElastiCache on old node types", OldCacheNodes, license.PlanFree},
	{"dynamodb-no-autoscaling", "DynamoDB tables paying for their peak all day", DynamoDBNoAutoScaling, license.PlanFree},
	{"fast-snapshot-restore", "Snapshots with Fast Snapshot Restore left on", FastSnapshotRestore, license.PlanFree},
	{"backup-orphans", "Backups of resources that no longer exist", BackupOrphans, license.PlanFree},
	{"efs-no-mount-targets", "EFS file systems nothing can reach", EFSNoMountTargets, license.PlanFree},
	{"unused-ami", "AMIs nobody launches", UnusedAMIs, license.PlanFree},
	{"idle-dedicated-host", "Dedicated Hosts running no instances", IdleDedicatedHosts, license.PlanFree},
	{"disabled-kms-key", "KMS keys switched off but still billed", DisabledKMSKeys, license.PlanFree},
	{"unused-private-ca", "Private CAs billed while they can't issue", UnusedPrivateCAs, license.PlanFree},
	{"rds-orphan-snapshot", "RDS snapshots of deleted databases", RDSOrphanSnapshots, license.PlanFree},
	{"empty-eks-cluster", "EKS clusters with nothing to run on", EmptyEKSClusters, license.PlanFree},
	{"nat-no-routes", "NAT gateways no route uses", NATNoRoutes, license.PlanFree},
	{"idle-classic-lb", "Classic Load Balancers with no instance in service", IdleClassicLBs, license.PlanFree},
	{"rds-old-class", "RDS databases on previous-generation classes", RDSOldClass, license.PlanFree},
	{"s3-versions-no-expiry", "Versioned buckets that keep every old version", S3VersionsNoExpiry, license.PlanFree},
	{"r53-unused-health-check", "Route 53 health checks nothing uses", UnusedHealthChecks, license.PlanFree},
	{"empty-hosted-zone", "Route 53 hosted zones with no records", EmptyHostedZones, license.PlanFree},
	{"orphan-alarm", "CloudWatch alarms on resources that are gone", OrphanAlarms, license.PlanFree},
	{"idle-dms-instance", "Idle DMS replication instances and Serverless replications", IdleDMS, license.PlanFree},
	{"unused-workspace", "WorkSpaces nobody signed in to for 30 days", UnusedWorkSpaces, license.PlanFree},
	{"unused-capacity-reservation", "EC2 Capacity Reservations nobody uses", UnusedCapacityReservations, license.PlanFree},
	{"ec2-detailed-monitoring", "EC2 detailed monitoring nobody needs", DetailedMonitoring, license.PlanFree},
	{"rds-graviton", "RDS databases that could run on Graviton", RDSGraviton, license.PlanFree},
	{"api-cache-unused", "API Gateway caches no method uses", APICacheUnused, license.PlanFree},
	{"cloudtrail-duplicate", "CloudTrail trails delivering a second copy of management events", DuplicateTrails, license.PlanFree},

	{"idle-nat", "NAT gateways with (almost) no traffic", nil, license.PlanPro},
	{"quiet-load-balancer", "Load balancers with targets but no traffic", nil, license.PlanPro},
	{"eks-extended-support", "EKS clusters paying for extended support", nil, license.PlanPro},
	{"rds-extended-support", "RDS and Aurora on engine versions past standard support", nil, license.PlanPro},
	{"elasticache-extended-support", "ElastiCache Redis OSS paying for extended support", nil, license.PlanPro},
	{"idle-dynamodb", "DynamoDB tables with provisioned capacity and no traffic", nil, license.PlanPro},
	{"idle-dynamodb-index", "DynamoDB indexes nobody reads", nil, license.PlanPro},
	{"dynamodb-table-class", "DynamoDB tables on the costlier table class", nil, license.PlanPro},
	{"quiet-accelerator", "Global Accelerators with no traffic", nil, license.PlanPro},
	{"lambda-errors", "Lambda functions whose calls keep failing", nil, license.PlanPro},
	{"idle-provisioned-concurrency", "Lambda provisioned concurrency nobody uses", nil, license.PlanPro},
	{"aurora-serverless-pause", "Aurora Serverless v2 clusters that could pause when idle", nil, license.PlanPro},
	{"idle-ecs-service", "ECS services with running tasks and no load balancer traffic", nil, license.PlanPro},
	{"fargate-oversized", "Fargate services sized bigger than their busiest task needs", nil, license.PlanPro},
	{"elasticache-oversized", "ElastiCache clusters a node size bigger than their peak load needs", nil, license.PlanPro},
	{"custom-metrics", "CloudWatch custom metrics: where the monitoring bill comes from", nil, license.PlanPro},
	{"s3-standard-no-tiering", "Large S3 Standard buckets without tiering", nil, license.PlanPro},
	{"idle-efs", "EFS file systems nobody connects to", nil, license.PlanPro},
	{"idle-kinesis", "Kinesis streams with no records", nil, license.PlanPro},
	{"opensearch-extended-support", "OpenSearch and Elasticsearch domains past standard support", nil, license.PlanPro},
	{"idle-opensearch", "OpenSearch domains nobody searches or writes to", nil, license.PlanPro},
	{"idle-transfer-server", "Stopped or idle Transfer Family servers", nil, license.PlanPro},
	{"idle-resolver-endpoint", "Route 53 Resolver endpoints with no queries", nil, license.PlanPro},
	{"idle-dx-connection", "Direct Connect connections that carry nothing or are down", nil, license.PlanPro},
	{"lightsail-waste", "Stopped or unattached Lightsail resources and load balancers with no instances", nil, license.PlanPro},
	{"idle-appstream-fleet", "AppStream fleets with no sessions", nil, license.PlanPro},
	{"idle-osi-pipeline", "OpenSearch Ingestion pipelines with no incoming records", nil, license.PlanPro},
	{"idle-mq", "Amazon MQ brokers with no messaging activity", nil, license.PlanPro},
	{"idle-apprunner-service", "App Runner services with no requests", nil, license.PlanPro},
	{"idle-docdb-elastic", "DocumentDB elastic clusters with no connections or operations", nil, license.PlanPro},
	{"unused-shield-advanced", "Shield Advanced subscriptions with no protected resources", nil, license.PlanPro},
	{"cloudfront-dedicated-ip-ssl", "CloudFront custom certificates using dedicated-IP SSL", nil, license.PlanPro},
	{"idle-fsx", "FSx file systems nobody reads or writes", nil, license.PlanPro},
	{"idle-opensearch-serverless", "OpenSearch Serverless collection groups and collections nobody uses", nil, license.PlanPro},
	{"idle-sagemaker-endpoint", "SageMaker endpoints with no invocations", nil, license.PlanPro},
	{"idle-bedrock-provisioned-throughput", "Bedrock Provisioned Throughput with no invocations", nil, license.PlanPro},
	{"stale-synthetics-canary", "Synthetics canaries failing every run", nil, license.PlanPro},
	{"idle-sagemaker-notebook", "SageMaker notebook instances to review", nil, license.PlanPro},
	{"idle-kendra-index", "Kendra indexes with no queries", nil, license.PlanPro},
	{"idle-glue-session", "Glue interactive sessions and development endpoints left READY", nil, license.PlanPro},
	{"opensearch-oversized", "OpenSearch data nodes with room to downsize", nil, license.PlanPro},
	{"idle-emr-serverless-app", "EMR Serverless applications paying for warm workers with no job runs", nil, license.PlanPro},
	{"idle-neptune-graph", "Neptune Analytics graphs with no queries", nil, license.PlanPro},
	{"keyspaces-provisioned-unused", "Keyspaces tables with underused provisioned throughput", nil, license.PlanPro},
	{"redshift-oversized", "Provisioned Redshift clusters with room to downsize", nil, license.PlanPro},
	{"idle-redshift", "Redshift clusters nobody connects to", nil, license.PlanPro},
	{"idle-tgw-attachment", "Transit Gateway attachments with no traffic", nil, license.PlanPro},
	{"idle-elasticache", "ElastiCache clusters nobody reads or writes", nil, license.PlanPro},
	{"idle-msk", "MSK clusters with no messages", nil, license.PlanPro},
	{"idle-client-vpn", "Client VPN endpoints nobody connects to", nil, license.PlanPro},
	{"vpn-tunnels-down", "Site-to-Site VPN connections with every tunnel down", nil, license.PlanPro},
	{"idle-rds", "RDS databases nobody connects to", nil, license.PlanPro},
	{"idle-db-cluster", "Aurora, Multi-AZ, DocumentDB and Neptune DB clusters nobody connects to", nil, license.PlanPro},
	{"quiet-classic-lb", "Classic Load Balancers with no traffic", nil, license.PlanPro},
	{"idle-network-firewall", "Network Firewalls with no traffic", nil, license.PlanPro},
	{"idle-ec2", "EC2 instances idle every day", nil, license.PlanPro},
	{"quiet-api-cache", "API Gateway caches nothing uses", nil, license.PlanPro},
	{"idle-attached-ebs", "Disks attached to running instances with no reads or writes", nil, license.PlanPro},
	{"eks-overrequested", "EKS clusters whose nodes are held by requests pods don't use", nil, license.PlanPro},
	{"idle-emr", "EMR clusters that did no work", nil, license.PlanPro},
	{"idle-memorydb", "MemoryDB clusters nobody reads or writes", nil, license.PlanPro},
	{"log-ingest", "Log groups that ingest the most", nil, license.PlanPro},
	{"public-ipv4-in-use", "Public IPv4 on resources that may not need it", nil, license.PlanPro},
	{"idle-endpoint", "Interface and Gateway Load Balancer endpoints with no traffic", nil, license.PlanPro},
	{"cost-hub", "AWS Cost Optimization Hub: rightsize, idle, Graviton", nil, license.PlanPro},
	{"compute-optimizer", "AWS Compute Optimizer rightsizing (when the hub is off)", nil, license.PlanPro},
	{"commitments", "Savings Plans and Reserved Instances to buy", nil, license.PlanTeam},
	{"commitments-expiring", "Reserved Instances and Savings Plans about to end", nil, license.PlanTeam},
	{"off-hours", "Databases idle at the same hours every week", nil, license.PlanTeam},
}

// BuildHasPro is true in the official release (built with -tags pro), which
// contains the paid checks; source builds of the open-source tree have only
// the free ones, and the paid entries of All have no Run.
var BuildHasPro = false

// Included reports whether a check runs on a plan.
func (c Check) Included(plan string) bool {
	return license.Rank(plan) >= license.Rank(c.Plan)
}

// Count is how many checks All lists at exactly that plan (Free, Pro or Team).
func Count(plan string) int {
	n := 0
	for _, c := range All {
		if c.Plan == plan {
			n++
		}
	}
	return n
}

// Title returns a check's human title.
func Title(id string) string {
	for _, c := range All {
		if c.ID == id {
			return c.Title
		}
	}
	return id
}

// Run applies every check to a region.
func Run(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, c := range All {
		if c.Run != nil && c.Included(o.Plan) {
			for _, f := range c.Run(r, o) {
				out = append(out, withDefaults(f))
			}
		}
	}
	return out
}

func days(now time.Time, t *time.Time) int {
	if t == nil || t.IsZero() {
		return -1
	}
	return int(now.Sub(*t).Hours() / 24)
}

func ageText(now time.Time, t *time.Time) string {
	d := days(now, t)
	if d < 0 {
		return "unknown age"
	}
	return fmt.Sprintf("%s (%d days ago)", t.UTC().Format("2006-01-02"), d)
}

func tag(tags []ec2types.Tag, key string) (string, bool) {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value), true
		}
	}
	return "", false
}

// TookOver maps a check to the one whose findings it took over: the same
// resources, now reported under the new name. nat-no-routes reports the NAT
// gateways nothing routes to, which idle-nat reported before. Rules written
// for the old check keep matching, and the run-to-run diff and the savings
// ledger don't count the move as a fix.
var TookOver = map[string]string{"nat-no-routes": "idle-nat"}

// Families group checks that take turns reporting one resource: an attached
// volume is idle-attached-ebs's while it looks idle (Pro scans), and
// gp2-to-gp3's or piops-to-gp3's otherwise; a running instance is
// idle-ec2's while it looks idle, else ec2-graviton's (old-instance-type's
// families never overlap ec2-graviton's). A move inside a family is neither
// new nor fixed in the diff and the savings ledger; it's matched by account,
// region and resource ID (or a finding's own key), not the label, whose Name
// tag can change. unattached-ebs is in the volume family too: a disk kept
// when its instance was terminated still costs the same, so leaving the
// other checks for it is no fix.
var Families = map[string]string{"gp2-to-gp3": "ebs-volume", "piops-to-gp3": "ebs-volume", "idle-attached-ebs": "ebs-volume", "unattached-ebs": "ebs-volume",
	"idle-ec2": "ec2-instance", "ec2-graviton": "ec2-instance", "old-instance-type": "ec2-instance",
	"idle-dynamodb": "dynamodb-table", "dynamodb-table-class": "dynamodb-table",
	"idle-accelerator": "accelerator", "quiet-accelerator": "accelerator",
	"idle-db-cluster": "db-cluster", "aurora-serverless-pause": "db-cluster"}

// ParkedWhenStopped: checks that leave a resource alone while its instance is
// stopped, though it costs the same (idle-attached-ebs judges running
// instances only; stopped-instance prices the disks after its threshold). The
// value is the fact naming the instance. Its finding going away while that
// instance is stopped is no fix.
var ParkedWhenStopped = map[string]string{"idle-attached-ebs": "instance", "ec2-detailed-monitoring": "instance"}

// ParkedWhileClusterExists: checks that leave a resource alone while the EKS
// cluster its fact names still exists this run (Karpenter node pools have no
// AWS list API). The value is the fact naming the cluster. Its finding going
// away while that cluster is listed is no fix.
var ParkedWhileClusterExists = map[string]string{"ec2-detailed-monitoring": "eks_cluster"}

func label(id string, tags []ec2types.Tag) string {
	if name, ok := tag(tags, "Name"); ok && name != "" {
		return fmt.Sprintf("%s (%s)", id, name)
	}
	return id
}

func volumeMonthly(r *inventory.Region, v ec2types.Volume) float64 {
	return pb(r).EBSMonthly(string(v.VolumeType), aws.ToInt32(v.Size), aws.ToInt32(v.Iops), aws.ToInt32(v.Throughput))
}

// stoppedInstanceVolumes maps volume ID -> instance ID for every volume attached
// to an instance the StoppedInstances check reports, so other checks don't count
// the same money twice.
func stoppedInstanceVolumes(r *inventory.Region, o Options) map[string]string {
	m := map[string]string{}
	for _, in := range r.Instances {
		if !stoppedLongEnough(in, o) {
			continue
		}
		for _, bd := range in.BlockDeviceMappings {
			if bd.Ebs != nil && bd.Ebs.VolumeId != nil {
				m[*bd.Ebs.VolumeId] = aws.ToString(in.InstanceId)
			}
		}
	}
	return m
}

// UnattachedVolumes: volumes in state "available" are billed in full and used by nothing.
func UnattachedVolumes(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, v := range r.Volumes {
		if v.State != ec2types.VolumeStateAvailable {
			continue
		}
		id := aws.ToString(v.VolumeId)
		f := Finding{
			Evidence: []string{
				"EC2 DescribeVolumes: state available (attached to nothing)",
				"created " + ageText(o.Now, v.CreateTime),
			},
			Check:      "unattached-ebs",
			Region:     r.Name,
			Resource:   label(id, v.Tags),
			Detail:     fmt.Sprintf("%s, %d GiB, attached to nothing; created %s", v.VolumeType, aws.ToInt32(v.Size), ageText(o.Now, v.CreateTime)),
			MonthlyUSD: volumeMonthly(r, v),
			Priced:     true,
			Fix: []string{
				fmt.Sprintf("aws ec2 create-snapshot --region %s --volume-id %s --description \"backup before delete\"", r.Name, id),
				fmt.Sprintf("aws ec2 delete-volume --region %s --volume-id %s", r.Name, id),
			},
			Facts: facts("volume", id, "az", aws.ToString(v.AvailabilityZone), "type", string(v.VolumeType), "size_gib", itoa(aws.ToInt32(v.Size)),
				"iops", nonZero(aws.ToInt32(v.Iops)), "throughput", nonZero(aws.ToInt32(v.Throughput))),
		}
		// A Kubernetes PersistentVolume is "available" whenever no pod uses it
		// (a StatefulSet scaled to zero): deleting it from AWS destroys the data
		// the cluster still points to.
		if pv, ok := tag(v.Tags, "kubernetes.io/created-for/pv/name"); ok {
			f.Confidence = "medium"
			f.Why = "a Kubernetes PersistentVolume with no pod right now; its workload may only be scaled to zero"
			f.Evidence = append(f.Evidence, "tag kubernetes.io/created-for/pv/name: "+pv)
			if pvc, ok := tag(v.Tags, "kubernetes.io/created-for/pvc/name"); ok {
				ns, _ := tag(v.Tags, "kubernetes.io/created-for/pvc/namespace")
				f.Evidence = append(f.Evidence, "claimed by PVC "+strings.Trim(ns+"/"+pvc, "/"))
				// Names the text uses, as facts: -redact hides what facts hold.
				if pvc != "" {
					f.Facts["kubernetes_pvc"] = pvc
				}
				if ns != "" {
					f.Facts["kubernetes_namespace"] = ns
				}
			}
			f.Facts["kubernetes_pv"] = pv
		}
		if name, kops := clusterOf(v.Tags); eksGone(r, name, kops) {
			f.Confidence = "medium"
			f.Why = "left behind: no EKS cluster named " + name + " in this region any more (deleted, or it ran outside EKS); a Retain reclaim policy keeps volumes on purpose, so snapshot first if unsure"
			f.Evidence = append(f.Evidence, "tagged for Kubernetes cluster "+name+"; EKS ListClusters: no cluster of that name in "+r.Name)
			f.Facts["kubernetes_cluster"] = name
		}
		out = append(out, f)
	}
	return out
}

// nonZero formats a count, or "" for zero (so facts() leaves it out).
func nonZero(v int32) string {
	if v == 0 {
		return ""
	}
	return itoa(v)
}

// GP2ToGP3: gp3 gives the same baseline performance as gp2 for ~20% less, and the
// switch is online (no downtime).
func GP2ToGP3(r *inventory.Region, o Options) []Finding {
	skip, idle := stoppedInstanceVolumes(r, o), idleEBS(r, o)
	var out []Finding
	for _, v := range r.Volumes {
		id := aws.ToString(v.VolumeId)
		if v.VolumeType != ec2types.VolumeTypeGp2 || v.State == ec2types.VolumeStateAvailable {
			continue // unattached gp2 volumes are reported in full by UnattachedVolumes
		}
		if _, ok := idle[id]; ok {
			continue // reported in full by idle-attached-ebs
		}
		if _, ok := skip[id]; ok {
			continue // reported in full by StoppedInstances
		}
		size := aws.ToInt32(v.Size)
		gp3, iops, thr := pb(r).GP3Match(size)
		saving := volumeMonthly(r, v) - gp3
		if saving < 0.005 {
			continue
		}
		out = append(out, Finding{
			Check:      "gp2-to-gp3",
			Region:     r.Name,
			Resource:   label(id, v.Tags),
			Detail:     fmt.Sprintf("gp2, %d GiB; gp3 with %d IOPS and %d MiB/s matches its speed for less, and the change is online", size, iops, thr),
			MonthlyUSD: saving,
			Priced:     true,
			Fix: []string{
				fmt.Sprintf("aws ec2 modify-volume --region %s --volume-id %s --volume-type gp3 --iops %d --throughput %d", r.Name, id, iops, thr),
			},
			Facts: facts("volume", id, "from_type", "gp2", "size_gib", itoa(size), "to_iops", itoa(iops), "to_throughput", itoa(thr)),
		})
	}
	return out
}

var stoppedAt = regexp.MustCompile(`\((\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) (?:GMT|UTC)\)`)

// stoppedSince parses "User initiated (2025-06-01 10:00:00 GMT)" (UTC accepted too).
func stoppedSince(in ec2types.Instance) *time.Time {
	m := stoppedAt.FindStringSubmatch(aws.ToString(in.StateTransitionReason))
	if m == nil {
		return nil
	}
	t, err := time.Parse("2006-01-02 15:04:05", m[1])
	if err != nil {
		return nil
	}
	return &t
}

func stoppedLongEnough(in ec2types.Instance, o Options) bool {
	if in.State == nil || in.State.Name != ec2types.InstanceStateNameStopped {
		return false
	}
	d := days(o.Now, stoppedSince(in))
	return d < 0 || d >= o.StoppedDays // unknown stop date counts
}

// StoppedInstances: a stopped instance costs nothing for compute but every
// attached EBS volume is still billed.
func StoppedInstances(r *inventory.Region, o Options) []Finding {
	vols := map[string]ec2types.Volume{}
	for _, v := range r.Volumes {
		vols[aws.ToString(v.VolumeId)] = v
	}
	var out []Finding
	for _, in := range r.Instances {
		if !stoppedLongEnough(in, o) {
			continue
		}
		id := aws.ToString(in.InstanceId)
		var cost float64
		var gib int32
		var ids []string
		n := 0
		for _, bd := range in.BlockDeviceMappings {
			if bd.Ebs == nil {
				continue
			}
			if v, ok := vols[aws.ToString(bd.Ebs.VolumeId)]; ok {
				cost += volumeMonthly(r, v)
				gib += aws.ToInt32(v.Size)
				ids = append(ids, aws.ToString(v.VolumeId))
				n++
			}
		}
		if n == 0 {
			continue
		}
		since := "an unknown date"
		if t := stoppedSince(in); t != nil {
			since = ageText(o.Now, t)
		}
		out = append(out, Finding{
			Check:      "stopped-instance",
			Region:     r.Name,
			Resource:   label(id, in.Tags),
			Detail:     fmt.Sprintf("%s, stopped since %s; %d volume(s), %d GiB still billed", in.InstanceType, since, n, gib),
			MonthlyUSD: cost,
			Priced:     true,
			Fix: []string{
				fmt.Sprintf("aws ec2 create-image --region %s --instance-id %s --name %s-backup-%s", r.Name, id, id, o.Now.UTC().Format("20060102")),
				fmt.Sprintf("aws ec2 terminate-instances --region %s --instance-ids %s", r.Name, id),
			},
			Facts: facts("instance", id, "type", string(in.InstanceType), "volumes", strings.Join(ids, " ")),
		})
	}
	return out
}

// IdleIPs: every public IPv4 is billed. An Elastic IP attached to nothing, or to
// a stopped instance, pays for nothing.
func IdleIPs(r *inventory.Region, o Options) []Finding {
	state := map[string]ec2types.InstanceStateName{}
	for _, in := range r.Instances {
		if in.State != nil {
			state[aws.ToString(in.InstanceId)] = in.State.Name
		}
	}
	var out []Finding
	for _, a := range r.Addresses {
		var why, onInstance string
		switch {
		// Empty strings count as missing: some AWS-compatible APIs return "" instead of omitting the field.
		case aws.ToString(a.AssociationId) == "" && aws.ToString(a.NetworkInterfaceId) == "" && aws.ToString(a.InstanceId) == "":
			why = "attached to nothing"
		case aws.ToString(a.InstanceId) != "" && state[aws.ToString(a.InstanceId)] == ec2types.InstanceStateNameStopped:
			onInstance = aws.ToString(a.InstanceId)
			why = "attached to stopped instance " + onInstance
		default:
			continue
		}
		ip := aws.ToString(a.PublicIp)
		alloc := aws.ToString(a.AllocationId)
		out = append(out, Finding{
			Check:      "idle-ip",
			Region:     r.Name,
			Resource:   label(ip, a.Tags),
			Detail:     fmt.Sprintf("Elastic IP %s; releasing it gives the address up for good, so check DNS and allowlists first", why),
			MonthlyUSD: pb(r).PublicIPv4Monthly(),
			Priced:     true,
			Fix:        []string{fmt.Sprintf("aws ec2 release-address --region %s --allocation-id %s", r.Name, alloc)},
			Facts:      facts("allocation", alloc, "ip", ip, "instance", onInstance),
		})
	}
	return out
}

// OldSnapshots groups old, unused snapshots by the volume they came from.
// Snapshots are incremental, so the money comes back only when every snapshot of
// a volume goes: if all of them are old and unused the finding is priced at the
// largest one; if newer ones are kept, deleting the old ones frees only the
// blocks nothing newer shares, which can't be known from the API, so it's unpriced.
func OldSnapshots(r *inventory.Region, o Options) []Finding {
	inAMI := map[string]bool{}
	for _, img := range r.Images {
		for _, bd := range img.BlockDeviceMappings {
			if bd.Ebs != nil && bd.Ebs.SnapshotId != nil {
				inAMI[*bd.Ebs.SnapshotId] = true
			}
		}
	}
	liveVolume := map[string]bool{}
	for _, v := range r.Volumes {
		liveVolume[aws.ToString(v.VolumeId)] = true
	}

	type lineage struct {
		old      []ec2types.Snapshot
		kept     int
		maxBytes float64
		archive  bool
	}
	groups := map[string]*lineage{}
	var order []string
	for _, s := range r.Snapshots {
		vol := aws.ToString(s.VolumeId)
		g, ok := groups[vol]
		if !ok {
			g = &lineage{}
			groups[vol] = g
			order = append(order, vol)
		}
		if !oldUnusedSnapshot(s, o, inAMI) {
			g.kept++
			continue
		}
		g.old = append(g.old, s)
		b := float64(aws.ToInt32(s.VolumeSize)) * (1 << 30)
		if s.FullSnapshotSizeInBytes != nil && *s.FullSnapshotSizeInBytes > 0 {
			b = float64(*s.FullSnapshotSizeInBytes)
		}
		if b > g.maxBytes {
			g.maxBytes = b
			g.archive = s.StorageTier == ec2types.StorageTierArchive
		}
	}

	var out []Finding
	for _, vol := range order {
		g := groups[vol]
		if len(g.old) == 0 {
			continue
		}
		sort.Slice(g.old, func(i, j int) bool { return aws.ToTime(g.old[i].StartTime).Before(aws.ToTime(g.old[j].StartTime)) })
		source := "source volume " + vol + " is deleted"
		if vol == "" || vol == "vol-ffffffff" {
			source = "copied or made from an AMI"
		} else if liveVolume[vol] {
			source = "source volume " + vol + " still exists"
		}
		f := Finding{
			Check:    "old-snapshots",
			Region:   r.Name,
			Resource: fmt.Sprintf("%d snapshot(s) of %s", len(g.old), orDash(vol)),
			Key:      "snapshots-of-" + orDash(vol),
			Detail: fmt.Sprintf("%s; oldest %s, newest %s; not used by any of your AMIs, and not made by AWS Backup or Data Lifecycle Manager",
				source, ageText(o.Now, g.old[0].StartTime), ageText(o.Now, g.old[len(g.old)-1].StartTime)),
		}
		gib := g.maxBytes / (1 << 30)
		if g.kept == 0 {
			price := pb(r).SnapshotGB
			if g.archive {
				price = pb(r).SnapshotArchiveGB
			}
			f.MonthlyUSD, f.Priced = gib*price, true
		} else {
			f.Detail += fmt.Sprintf("; %d newer snapshot(s) of this volume are kept, so deleting these frees only what they don't share", g.kept)
		}
		var ids []string
		for _, s := range g.old {
			f.Fix = append(f.Fix, fmt.Sprintf("aws ec2 delete-snapshot --region %s --snapshot-id %s", r.Name, aws.ToString(s.SnapshotId)))
			ids = append(ids, aws.ToString(s.SnapshotId))
		}
		f.Facts = facts("volume", vol, "snapshots", strings.Join(ids, " "))
		out = append(out, f)
	}
	return out
}

func oldUnusedSnapshot(s ec2types.Snapshot, o Options, inAMI map[string]bool) bool {
	if s.State != ec2types.SnapshotStateCompleted || inAMI[aws.ToString(s.SnapshotId)] {
		return false
	}
	// Snapshots owned by AWS Backup or Data Lifecycle Manager are deleted by their
	// own policy; deleting them by hand fights the policy.
	if _, ok := tag(s.Tags, "aws:backup:source-resource"); ok {
		return false
	}
	if _, ok := tag(s.Tags, "aws:dlm:lifecycle-policy-id"); ok {
		return false
	}
	if strings.Contains(aws.ToString(s.Description), "AWS Backup service") {
		return false
	}
	d := days(o.Now, s.StartTime)
	return d >= o.SnapshotAgeDays
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// LogRetention: log groups default to "never expire", so stored logs grow forever.
func LogRetention(r *inventory.Region, o Options) []Finding {
	var out []Finding
	var smallN int
	var smallGiB, smallUSD float64
	for _, g := range r.LogGroups {
		if g.RetentionInDays != nil {
			continue
		}
		gib := float64(aws.ToInt64(g.StoredBytes)) / (1 << 30)
		name := aws.ToString(g.LogGroupName)
		rate, assumedStandard := pricing.LogsStoredRate(string(g.LogGroupClass), pb(r).LogsStorageGB)
		if gib < o.MinLogGiB {
			smallN++
			smallGiB += gib
			smallUSD += gib * rate
			continue
		}
		detail := fmt.Sprintf("%.1f GiB stored, never expires; a retention policy deletes data older than the days you pick", gib)
		if assumedStandard {
			detail += fmt.Sprintf("; priced at the Standard storage rate ($%.3f/GB-month); Infrequent Access is $%.3f and Archive Instant Access is $%.3f when AWS reports those tiers",
				rate, pricing.LogsStoredIAGBMonth, pricing.LogsStoredArchiveInstantGBMonth)
		}
		f := Finding{
			Check:      "log-retention",
			Region:     r.Name,
			Resource:   name,
			Detail:     detail,
			MonthlyUSD: gib * rate,
			Priced:     true,
			Fix:        []string{fmt.Sprintf("aws logs put-retention-policy --region %s --log-group-name '%s' --retention-in-days 30", r.Name, name)},
			Facts:      facts("log_group", name, "days", "30"),
		}
		// The control-plane logs of a cluster EKS no longer has: nothing new
		// arrives, so the whole group is the waste, not only its old part.
		if c, ok := eksLogGroup(name); ok && eksGone(r, c, false) {
			f.Why = "left behind: no EKS cluster named " + c + " in this region any more, so nothing new arrives; keep what your audit rules need, then delete it"
			f.Evidence = append(f.Evidence, "EKS writes a cluster's control-plane logs to /aws/eks/<cluster>/cluster; EKS ListClusters: no cluster named "+c+" in "+r.Name)
			f.Fix = []string{
				"# its EKS cluster is gone, so nothing new arrives: export what your audit rules need (to S3), then",
				fmt.Sprintf("aws logs delete-log-group --region %s --log-group-name '%s'", r.Name, name),
				"# or let it drain over the days your audit rules ask for: " + f.Fix[0],
			}
			f.Facts["kubernetes_cluster"] = c // named in the text: -redact hides what facts hold
		}
		out = append(out, f)
	}
	if smallN > 0 {
		out = append(out, Finding{
			Check:      "log-retention",
			Region:     r.Name,
			Resource:   fmt.Sprintf("%d smaller log group(s)", smallN),
			Key:        "small-never-expiring-log-groups",
			Detail:     fmt.Sprintf("%.2f GiB stored in total, never expire; cheap today, but they keep growing; priced at each group's storage class (Standard when the class isn't reported)", smallGiB),
			MonthlyUSD: smallUSD,
			Priced:     true,
		})
	}
	return out
}

// IdleLoadBalancers: a load balancer with no registered targets still pays its
// hourly charge.
func IdleLoadBalancers(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, lb := range r.LoadBalancers {
		if lb.Targets > 0 {
			continue
		}
		why := fmt.Sprintf("%d target group(s), 0 registered targets", lb.TargetGroups)
		if lb.TargetGroups == 0 {
			why = "no target groups"
		}
		targets := why
		switch {
		case lb.Responds > 0:
			why += fmt.Sprintf("; %d listener(s) redirect or return a fixed response", lb.Responds)
		case lb.ListenersUnknown:
			why += "; its listeners couldn't be read"
		}
		keys := make([]string, 0, len(lb.TargetGroupARNs))
		for _, tg := range lb.TargetGroupARNs {
			keys = append(keys, "tg:"+tg)
		}
		asgs, refill, gone := lbMayRefill(r, keys, lb.Kubernetes, lb.TagsUnknown)
		del := fmt.Sprintf("aws elbv2 delete-load-balancer --region %s --load-balancer-arn %s", r.Name, lb.ARN)
		f := Finding{
			Check:      "idle-load-balancer",
			Region:     r.Name,
			Resource:   lb.Name,
			Detail:     fmt.Sprintf("%s load balancer, %s; check DNS records pointing at it before deleting", lb.Type, why),
			MonthlyUSD: pb(r).LoadBalancerMonthly(lb.Type),
			Priced:     true,
			// asgs: the Auto Scaling groups that register with its target groups, for the
			// reader (the lb-delete fix checks Auto Scaling live, not this fact).
			Facts: facts("lb_arn", lb.ARN, "lb_name", lb.Name, "lb_type", lb.Type, "asgs", strings.Join(asgs, " ")),
		}
		f.Evidence = append(f.Evidence, "ELB DescribeTargetGroups and DescribeTargetHealth: "+targets)
		if lb.Type == "application" { // the listeners of an ALB with no targets are read
			switch {
			case lb.Responds > 0:
				f.Evidence = append(f.Evidence, fmt.Sprintf("ELB DescribeListeners and DescribeRules: %d listener(s) with a redirect or fixed-response action", lb.Responds))
			case lb.ListenersUnknown:
				f.Evidence = append(f.Evidence, "ELB DescribeListeners and DescribeRules: couldn't be read")
			default:
				f.Evidence = append(f.Evidence, "ELB DescribeListeners and DescribeRules: no listener redirects or returns a fixed response")
			}
		}
		if lb.Scheme == "internet-facing" && (lb.Type == "application" || lb.Type == "network") {
			f.Detail += fmt.Sprintf("; it also pays %s a month for each public IPv4 address (one per zone it's in), which this price leaves out", money(pb(r).PublicIPv4Monthly()))
		}
		if k := lb.Kubernetes; k != "" && k != "?" { // named in the Why: -redact hides what facts hold
			f.Facts["kubernetes_cluster"] = k
		}
		if refill != "" {
			f.Confidence, f.Why = "low", refill
			del = "# " + del
		}
		var call string // a person's call, ahead of the commented delete
		switch answer := "a redirect (HTTP to HTTPS, or from an old domain) or a fixed response answers users without a target"; {
		case lb.Responds > 0:
			if f.Confidence != "low" {
				del = "# " + del
			}
			call = "# a person's call: check that nobody relies on its redirects or fixed responses first"
			f.Confidence, f.Why = "low", "it may still be in use: "+answer+joinWhy(f.Why)
		case lb.ListenersUnknown && f.Confidence == "":
			del = "# " + del // below high, the delete is a comment (as idle-classic-lb's)
			call = "# a person's call: check its listeners for redirects or fixed responses first"
			f.Confidence, f.Why = "medium", "its listeners couldn't be read: "+answer
		}
		if gone != "" {
			f.Evidence = append(f.Evidence, "tagged for Kubernetes cluster "+gone+"; EKS ListClusters: no cluster of that name in "+r.Name)
			left := "left behind by Kubernetes cluster " + gone + ", which isn't in this region's EKS any more: nothing will register targets again"
			if f.Why == "" {
				f.Why = left + ", so delete the load balancer itself (no Service is left to delete)"
			} else {
				f.Why += "; " + left // its listener reason, which set the confidence, stays first
			}
		}
		f.Fix = []string{del}
		if call != "" {
			f.Fix = []string{call, del}
		}
		out = append(out, f)
	}
	return out
}

// NATWithoutS3Endpoint: S3 traffic from private subnets goes through the NAT
// gateway at ~$0.045/GB unless the VPC has the free S3 gateway endpoint.
func NATWithoutS3Endpoint(r *inventory.Region, o Options) []Finding {
	nats := map[string]int{}
	var vpcs []string
	for _, n := range r.NatGateways {
		if n.State != ec2types.NatGatewayStateAvailable {
			continue
		}
		vpc := aws.ToString(n.VpcId)
		if nats[vpc] == 0 {
			vpcs = append(vpcs, vpc)
		}
		nats[vpc]++
	}
	hasS3 := map[string]bool{}
	for _, e := range r.VpcEndpoints {
		if e.VpcEndpointType == ec2types.VpcEndpointTypeGateway &&
			strings.HasSuffix(aws.ToString(e.ServiceName), ".s3") &&
			strings.EqualFold(string(e.State), "available") {
			hasS3[aws.ToString(e.VpcId)] = true
		}
	}
	var out []Finding
	for _, vpc := range vpcs {
		if hasS3[vpc] {
			continue
		}
		out = append(out, Finding{
			Check:    "nat-no-s3-endpoint",
			Region:   r.Name,
			Resource: vpc,
			Detail:   fmt.Sprintf("%d NAT gateway(s) but no S3 gateway endpoint: S3 traffic from private subnets pays NAT data charges; the endpoint is free", nats[vpc]),
			Fix: []string{
				fmt.Sprintf("aws ec2 create-vpc-endpoint --region %s --vpc-id %s --vpc-endpoint-type Gateway --service-name com.amazonaws.%s.s3 --route-table-ids <your private route table ids>", r.Name, vpc, r.Name),
			},
			Facts: facts("vpc", vpc, "service", "com.amazonaws."+r.Name+".s3"),
		})
	}
	return out
}

// oldFamilies maps old instance families to their current replacement.
var oldFamilies = map[string]string{
	"t1": "t3", "t2": "t3 (or t4g on ARM)",
	"m1": "m7i", "m2": "r7i", "m3": "m7i", "m4": "m7i (or m7g on ARM)",
	"c1": "c7i", "c3": "c7i", "c4": "c7i (or c7g on ARM)",
	"r3": "r7i", "r4": "r7i (or r7g on ARM)",
	"i2": "i4i", "d2": "d3", "g2": "g5", "g3": "g5", "p2": "g5", "x1": "x2iedn", "x1e": "x2iedn",
}

// OldInstanceTypes: old generations are slower, and usually cost more than a
// current type of the same size. Never priced (a move needs testing); for a
// plain Linux On-Demand c, m, r or t instance the Detail states what the
// usual pick and the cheapest current type at least as big, offered in its
// zone, cost an hour. EMR nodes are left out: EMR picks their types.
func OldInstanceTypes(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, in := range r.Instances {
		if in.State == nil || in.State.Name != ec2types.InstanceStateNameRunning {
			continue
		}
		if j, ok := tag(in.Tags, "aws:elasticmapreduce:job-flow-id"); ok && j != "" {
			continue
		}
		t := string(in.InstanceType)
		family, _, _ := strings.Cut(t, ".")
		repl, ok := oldFamilies[family]
		if !ok {
			continue
		}
		out = append(out, Finding{
			Check:    "old-instance-type",
			Region:   r.Name,
			Resource: label(aws.ToString(in.InstanceId), in.Tags),
			Detail:   oldDetail(r, in, repl),
		})
	}
	return out
}

// oldDetail compares an old instance's hourly price with the usual pick
// (oldFamilies) and the cheapest current x86 type at least as big offered in
// its zone, all from one source; anything it can't price gets the plain
// wording, with no price claim.
func oldDetail(r *inventory.Region, in ec2types.Instance, repl string) string {
	typ := string(in.InstanceType)
	plain := fmt.Sprintf("%s is an old generation; %s is the current one: compare their speed and price before moving", typ, repl)
	if r.Offered == nil || !inventory.LinuxOnDemand(in, r.Name) {
		return plain
	}
	az := aws.ToString(in.Placement.AvailabilityZone)
	var opts []string
	for _, t := range pricing.EC2CurrentOptions(typ) {
		if r.Offered[az][t] {
			opts = append(opts, t)
		}
	}
	if len(opts) == 0 {
		return plain
	}
	p, source := ec2Prices(r, append([]string{typ}, opts...))
	if p == nil {
		return plain
	}
	slices.SortStableFunc(opts, func(a, b string) int { return cmp.Compare(p[a], p[b]) })
	cheap, now := opts[0], p[typ]
	_, size, _ := strings.Cut(typ, ".")
	usual := strings.Fields(repl)[0] + "." + size
	cpu := func(t string) string {
		if s, ok := pricing.EC2(t); ok && s.CPU != "" {
			return " (" + s.CPU + ")"
		}
		return ""
	}
	than := func(v float64) string {
		switch {
		case v < now:
			return ", less than now"
		case v > now:
			return ", more than now"
		}
		return ", the same as now"
	}
	var b strings.Builder
	var named []string
	fmt.Fprintf(&b, "%s is an old generation ($%.4f an hour, %s)", typ, now, source)
	if slices.Contains(opts, usual) {
		named = append(named, usual)
		switch d := p[usual] - now; {
		case d > 0:
			fmt.Fprintf(&b, ": %s, the usual pick, costs $%.4f more an hour", usual, d)
		case d < 0:
			fmt.Fprintf(&b, ": %s, the usual pick, costs $%.4f less an hour", usual, -d)
		default:
			fmt.Fprintf(&b, ": %s, the usual pick, costs the same", usual)
		}
		if cheap != usual && p[cheap] < p[usual] {
			named = append(named, cheap)
			fmt.Fprintf(&b, "; %s%s costs $%.4f%s", cheap, cpu(cheap), p[cheap], than(p[cheap]))
		}
	} else {
		named = append(named, cheap)
		fmt.Fprintf(&b, ": %s%s, the cheapest current type at least as big offered in its zone, costs $%.4f%s", cheap, cpu(cheap), p[cheap], than(p[cheap]))
	}
	// "At least as big" is vCPUs and memory: an old type's instance storage
	// may have no counterpart.
	if s, ok := pricing.EC2(typ); ok && s.StoreGB > 0 {
		for _, t := range named {
			if c, ok := pricing.EC2(t); ok && c.StoreGB < s.StoreGB {
				fmt.Fprintf(&b, "; %s has less instance storage than its %d GB (data kept there would move to EBS)", t, s.StoreGB)
			}
		}
	}
	return b.String()
}

// joinWhy appends an earlier reason to a new one.
func joinWhy(why string) string {
	if why == "" {
		return ""
	}
	return "; " + why
}
