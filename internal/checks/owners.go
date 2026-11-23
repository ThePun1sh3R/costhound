package checks

import "strings"

// tagRefs says which resource each check's findings are about, so the owner
// lookup (internal/owner) can read its tags: Finding.TagRef, the resource as
// its ARN names it after the account. A check whose findings sum up several
// resources, or are about none, is in noTagRef with the reason instead; a
// test makes sure every check is in one of the two.
var tagRefs = map[string]func(f Finding, derived bool) string{
	"idle-dms-instance":            byARN("arn"),
	"unused-workspace":             byFact("workspaces:workspace/", "workspace"),
	"idle-transfer-server":         byFact("transfer:server/", "transfer_server"),
	"idle-resolver-endpoint":       byFact("route53resolver:resolver-endpoint/", "resolver_endpoint"),
	"idle-fsx":                     byARN("arn"),
	"unattached-ebs":               byKey("ec2:volume/"),
	"gp2-to-gp3":                   byKey("ec2:volume/"),
	"piops-to-gp3":                 byKey("ec2:volume/"),
	"io1-to-io2":                   byKey("ec2:volume/"),
	"api-cache-unused":             byKey("apigateway:restapis/"),
	"cloudtrail-duplicate":         byARN("arn"),
	"idle-attached-ebs":            byKey("ec2:volume/"),
	"eks-overrequested":            byKey("eks:cluster/"),
	"idle-emr":                     byKey("elasticmapreduce:cluster/"),
	"idle-memorydb":                byARN("arn"),
	"quiet-api-cache":              byKey("apigateway:restapis/"),
	"stopped-instance":             byKey("ec2:instance/"),
	"old-instance-type":            byKey("ec2:instance/"),
	"idle-ec2":                     byKey("ec2:instance/"),
	"ec2-graviton":                 byFirstFact("ec2:instance/", "instance", "sample"), // an Auto Scaling group: one of its instances
	"idle-ip":                      byFact("ec2:elastic-ip/", "allocation"),
	"log-retention":                byKey("logs:log-group:"),
	"log-ingest":                   byKey("logs:log-group:"),
	"idle-load-balancer":           byARN("lb_arn"),
	"quiet-load-balancer":          byARN("lb_arn"),
	"nat-no-s3-endpoint":           byKey("ec2:vpc/"), // the VPC's owner decides on its endpoints
	"paid-s3-endpoint":             byKey("ec2:vpc-endpoint/"),
	"idle-endpoint":                byKey("ec2:vpc-endpoint/"),
	"unused-secret":                byKey("secretsmanager:secret:"),
	"ecr-cleanup":                  byKey("ecr:repository/"),
	"dynamodb-no-autoscaling":      byKey("dynamodb:table/"),
	"idle-dynamodb":                byKey("dynamodb:table/"),
	"idle-dynamodb-index":          byFact("dynamodb:table/", "table"), // an index has no tags: its table's
	"dynamodb-table-class":         byKey("dynamodb:table/"),
	"idle-accelerator":             byKey("globalaccelerator:accelerator/"),
	"quiet-accelerator":            byKey("globalaccelerator:accelerator/"),
	"lambda-errors":                byARN("arn"),
	"idle-provisioned-concurrency": byFact("lambda:function:", "function"), // tags are the function's
	"aurora-serverless-pause":      byFact("rds:cluster:", "cluster"),      // tags are the cluster's
	"fargate-oversized":            byARN("arn"),
	"fast-snapshot-restore":        byKey("ec2:snapshot/"),
	"efs-no-mount-targets":         byKey("elasticfilesystem:file-system/"),
	"idle-efs":                     byKey("elasticfilesystem:file-system/"),
	"unused-ami":                   byKey("ec2:image/"),
	"idle-dedicated-host":          byKey("ec2:dedicated-host/"),
	"disabled-kms-key":             byARN("key_arn"),
	"unused-private-ca":            byARN("ca"),
	"empty-eks-cluster":            byKey("eks:cluster/"),
	"eks-extended-support":         byKey("eks:cluster/"),
	"nat-no-routes":                byKey("ec2:natgateway/"),
	"idle-nat":                     byKey("ec2:natgateway/"),
	"rds-extended-support":         byKey("rds:db:"),
	"idle-kinesis":                 byKey("kinesis:stream/"),
	"opensearch-extended-support":  byKey("es:domain/"),
	"idle-opensearch":              byKey("es:domain/"),
	"idle-sagemaker-endpoint":      byKey("sagemaker:endpoint/"),
	"idle-redshift":                byKey("redshift:cluster:"),
	"idle-tgw-attachment":          byKey("ec2:transit-gateway-attachment/"),
	"idle-elasticache": func(f Finding, _ bool) string {
		return cacheRef(f.Facts["cache"], f.Facts["replication_group"] == "true")
	},
	"idle-msk":              byARN("cluster_arn"),
	"idle-client-vpn":       byKey("ec2:client-vpn-endpoint/"),
	"vpn-tunnels-down":      byKey("ec2:vpn-connection/"),
	"s3-versions-no-expiry": byKey("s3:"), // a bucket ARN names no type: arn:aws:s3:::<bucket>
	// Batch 5. A Classic Load Balancer's ARN is loadbalancer/<name>.
	"idle-rds":              byARN("arn"),
	"idle-db-cluster":       byARN("arn"),
	"rds-old-class":         byARN("arn"),
	"idle-classic-lb":       byKey("elasticloadbalancing:loadbalancer/"),
	"quiet-classic-lb":      byKey("elasticloadbalancing:loadbalancer/"),
	"idle-network-firewall": byARN("firewall_arn"),
	// Batch 6. A health check's ARN is arn:aws:route53:::healthcheck/<id>.
	"r53-unused-health-check":     byKey("route53:healthcheck/"),
	"empty-hosted-zone":           byFact("route53:hostedzone/", "zone_id"),
	"unused-capacity-reservation": byFact("ec2:capacity-reservation/", "capacity_reservation"),
	"ec2-detailed-monitoring":     byFirstFact("ec2:instance/", "instance", "sample"), // an Auto Scaling group: one of its instances
	"off-hours": func(f Finding, x bool) string {
		if f.Facts["kind"] == "redshift" {
			if c := f.Facts["cluster"]; c != "" {
				return "redshift:cluster:" + c
			}
			return ""
		}
		return byARN("arn")(f, x)
	},
	// An alarm's name can hold spaces, which a reference can't: its owner stays unknown.
	"orphan-alarm": func(f Finding, _ bool) string {
		if n := f.Facts["alarm"]; n != "" && !strings.ContainsAny(n, " \t") {
			return "cloudwatch:alarm:" + n
		}
		return ""
	},
	"rds-graviton": byARN("arn"),
	// Batch 7. ARN formats (service authorization reference): RDS ri:<id>,
	// ElastiCache reserved-instance:<id>, savingsplans::<account>:savingsplan/<id>
	// (from the SDK's ARN), EC2 reserved-instances/<id>; OpenSearch and
	// Redshift reservations have no resource type, so no ref.
	"commitments-expiring": func(f Finding, derived bool) string {
		if ref := byARN("arn")(f, derived); ref != "" {
			return ref
		}
		if f.Facts["kind"] == "EC2" {
			return byFact("ec2:reserved-instances/", "reservation")(f, derived)
		}
		return ""
	},
}

var noTagRef = map[string]string{
	"old-snapshots":       "a finding sums up every old snapshot of one disk",
	"lambda-x86":          "a finding sums up the region's x86 functions",
	"s3-abort-mpu":        "a finding sums up buckets",
	"public-ipv4-in-use":  "a finding sums up public IPv4 addresses",
	"rds-orphan-snapshot": "a finding sums up the snapshots of a deleted database",
	"backup-orphans":      "its resource is deleted",
	"custom-metrics":      "a finding is about a metric namespace",
	"cost-hub":            "Cost Optimization Hub findings come from outside the region scan",
	"commitments":         "a finding is a purchase, not a resource",
	// OpenSearch Serverless isn't known to answer tag:GetResources, so an
	// empty answer couldn't tell untagged from unknown.
	"idle-opensearch-serverless": "a finding is a billing pool, and collections' tags aren't read",
	// A bucket's ARN (arn:aws:s3:::<name>) has no resource type, which the
	// owner lookup's tag filter and ref shape need.
}

// selfTagRef: checks that set TagRef on their findings themselves (a cache
// is a cluster or a replication group, and the two have different ARNs).
var selfTagRef = map[string]bool{"old-cache-nodes": true, "elasticache-extended-support": true}

// byKey: the resource is the one the finding's label starts with (a finding
// that sets its own key sums up several resources and gets none).
func byKey(prefix string) func(Finding, bool) string {
	return func(f Finding, derived bool) string {
		if !derived {
			return ""
		}
		id := f.Resource
		if i := strings.IndexByte(id, ' '); i > 0 {
			id = id[:i]
		}
		if id == "" {
			return ""
		}
		return prefix + id
	}
}

// byFact: the resource's ID is in a fact.
func byFact(prefix, fact string) func(Finding, bool) string {
	return func(f Finding, _ bool) string {
		if v := f.Facts[fact]; v != "" {
			return prefix + v
		}
		return ""
	}
}

// byFirstFact: the first of the facts that is set holds the resource's ID.
func byFirstFact(prefix string, keys ...string) func(Finding, bool) string {
	return func(f Finding, _ bool) string {
		for _, k := range keys {
			if v := f.Facts[k]; v != "" {
				return prefix + v
			}
		}
		return ""
	}
}

// byARN: the resource's ARN is in a fact; the ref is what follows the account.
func byARN(fact string) func(Finding, bool) string {
	return func(f Finding, _ bool) string {
		p := strings.SplitN(f.Facts[fact], ":", 6)
		if len(p) != 6 || p[0] != "arn" {
			return ""
		}
		return p[2] + ":" + p[5]
	}
}

func cacheRef(name string, replicationGroup bool) string {
	if name == "" {
		return ""
	}
	if replicationGroup {
		return "elasticache:replicationgroup:" + name
	}
	return "elasticache:cluster:" + name
}
