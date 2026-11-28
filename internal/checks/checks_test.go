package checks

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func ago(d int) *time.Time { t := now.AddDate(0, 0, -d); return &t }

const gib = int64(1 << 30)

func vol(id string, typ ec2types.VolumeType, size int32, state ec2types.VolumeState) ec2types.Volume {
	return ec2types.Volume{VolumeId: aws.String(id), VolumeType: typ, Size: aws.Int32(size), State: state, CreateTime: ago(30)}
}

func inst(id string, typ ec2types.InstanceType, state ec2types.InstanceStateName, reason string, vols ...string) ec2types.Instance {
	in := ec2types.Instance{
		InstanceId:            aws.String(id),
		InstanceType:          typ,
		State:                 &ec2types.InstanceState{Name: state},
		StateTransitionReason: aws.String(reason),
	}
	for _, v := range vols {
		in.BlockDeviceMappings = append(in.BlockDeviceMappings, ec2types.InstanceBlockDeviceMapping{Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String(v)}})
	}
	return in
}

func snap(id, volID string, ageDays int, volGiB int32, fullGiB int64, state ec2types.SnapshotState, tags ...ec2types.Tag) ec2types.Snapshot {
	s := ec2types.Snapshot{
		SnapshotId: aws.String(id), VolumeId: aws.String(volID), StartTime: ago(ageDays),
		VolumeSize: aws.Int32(volGiB), State: state, Tags: tags,
	}
	if fullGiB > 0 {
		s.FullSnapshotSizeInBytes = aws.Int64(fullGiB * gib)
	}
	return s
}

func fixture() *inventory.Region {
	r := withLongTail(&inventory.Region{
		Name: "eu-west-1",
		Accelerators: []inventory.Accelerator{
			{ARN: "arn:aws:globalaccelerator::123456789012:accelerator/a0000000-0000-4000-8000-000000000001", ID: "a0000000-0000-4000-8000-000000000001", Name: "old-edge", Status: "DEPLOYED", Enabled: true,
				Created: ago(200), IPv4: 2, Listeners: 1, Groups: 1},
			{ARN: "arn:aws:globalaccelerator::123456789012:accelerator/a0000000-0000-4000-8000-000000000002", ID: "a0000000-0000-4000-8000-000000000002", Name: "quiet-edge", Status: "DEPLOYED", Enabled: true,
				Created: ago(200), IPv4: 2, Listeners: 1, Groups: 1, Endpoints: 2},
		},
		Offered: map[string]map[string]bool{
			"eu-west-1a": {"m6g.large": true, "m7g.large": true},
			"eu-west-1b": {"m6g.large": true, "m7g.large": true},
		},
		Volumes: []ec2types.Volume{
			piops("vol-io1", "io1", 500, 8000),
			piops("vol-io2-fast", "io2", 100, 20000), // fits gp3 now (80,000 IOPS), but io2 can deliver more throughput
			piops("vol-io1-dense", "io1", 10, 6000),  // over gp3's 500 IOPS per GiB
			func() ec2types.Volume {
				v := vol("vol-unatt", ec2types.VolumeTypeGp2, 100, ec2types.VolumeStateAvailable)
				v.Tags = []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("old-data")}}
				return v
			}(),
			vol("vol-run", ec2types.VolumeTypeGp2, 100, ec2types.VolumeStateInUse),
			vol("vol-stop", ec2types.VolumeTypeGp2, 50, ec2types.VolumeStateInUse),
			vol("vol-stopnew", ec2types.VolumeTypeGp2, 20, ec2types.VolumeStateInUse),
			vol("vol-gp3", ec2types.VolumeTypeGp3, 500, ec2types.VolumeStateInUse),
			piops("vol-io1-big", "io1", 1000, 48000), // over the old 16,000 limit: low, and io2 is cheaper too
			func() ec2types.Volume { // gp3 can't take a Multi-Attach volume: io1-to-io2
				v := piops("vol-io1-ma", "io1", 1000, 40000)
				v.MultiAttachEnabled = aws.Bool(true)
				return v
			}(),
			func() ec2types.Volume { // on an Outpost gp3 stops at 16,000 IOPS
				v := piops("vol-io1-outpost", "io1", 1000, 20000)
				v.OutpostArn = aws.String("arn:aws:outposts:eu-west-1:123456789012:outpost/op-0abc")
				return v
			}(),
			func() ec2types.Volume { // fits an Outpost's gp3 (throughput up to 1,000)
				v := piops("vol-io1-outpost-ok", "io1", 200, 12000)
				v.OutpostArn = aws.String("arn:aws:outposts:eu-west-1:123456789012:outpost/op-0abc")
				return v
			}(),
			func() ec2types.Volume { // io2 on an Outpost isn't in the EBS docs: left out
				v := piops("vol-io1-ma-outpost", "io1", 1000, 40000)
				v.MultiAttachEnabled, v.OutpostArn = aws.Bool(true), aws.String("arn:aws:outposts:eu-west-1:123456789012:outpost/op-0abc")
				return v
			}(),
		},
		Instances: []ec2types.Instance{
			inst("i-run", "t2.micro", ec2types.InstanceStateNameRunning, "", "vol-run"),
			inst("i-stop", "m5.large", ec2types.InstanceStateNameStopped, "User initiated (2026-10-01 10:00:00 GMT)", "vol-stop"),
			inst("i-stopnew", "m5.large", ec2types.InstanceStateNameStopped, "User initiated (2026-11-16 10:00:00 GMT)", "vol-stopnew"),
			withMonitoring(inst("i-modern", "m7i.large", ec2types.InstanceStateNameRunning, "", "vol-gp3")),
			inst("i-oldstopped", "t2.small", ec2types.InstanceStateNameStopped, "User initiated (2026-11-16 10:00:00 GMT)"),
			x86("i-x86-web", "m5.large", "eu-west-1a"),
			x86("i-web-1", "m6i.large", "eu-west-1a", "aws:autoscaling:groupName", "web-x86"),
			x86("i-web-2", "m6i.large", "eu-west-1b", "aws:autoscaling:groupName", "web-x86"),
		},
		Addresses: []ec2types.Address{
			{AllocationId: aws.String("eipalloc-free"), PublicIp: aws.String("203.0.113.1")},
			{AllocationId: aws.String("eipalloc-run"), PublicIp: aws.String("203.0.113.2"), AssociationId: aws.String("a1"), InstanceId: aws.String("i-run")},
			{AllocationId: aws.String("eipalloc-stop"), PublicIp: aws.String("203.0.113.3"), AssociationId: aws.String("a2"), InstanceId: aws.String("i-stop")},
			{AllocationId: aws.String("eipalloc-nat"), PublicIp: aws.String("203.0.113.4"), AssociationId: aws.String("a3"), NetworkInterfaceId: aws.String("eni-nat")},
		},
		Snapshots: []ec2types.Snapshot{
			snap("snap-a", "vol-gone", 400, 8, 4, ec2types.SnapshotStateCompleted),
			snap("snap-b", "vol-gone", 200, 8, 6, ec2types.SnapshotStateCompleted),
			snap("snap-c", "vol-run", 300, 100, 0, ec2types.SnapshotStateCompleted),
			snap("snap-d", "vol-run", 10, 100, 0, ec2types.SnapshotStateCompleted),
			snap("snap-ami", "vol-ami", 500, 30, 0, ec2types.SnapshotStateCompleted),
			snap("snap-backup", "vol-bk", 500, 30, 0, ec2types.SnapshotStateCompleted,
				ec2types.Tag{Key: aws.String("aws:backup:source-resource"), Value: aws.String("x")}),
			snap("snap-dlm", "vol-dlm", 500, 30, 0, ec2types.SnapshotStateCompleted,
				ec2types.Tag{Key: aws.String("aws:dlm:lifecycle-policy-id"), Value: aws.String("p")}),
			snap("snap-pending", "vol-pend", 500, 30, 0, ec2types.SnapshotStatePending),
		},
		Images: []ec2types.Image{{ImageId: aws.String("ami-1"), BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{Ebs: &ec2types.EbsBlockDevice{SnapshotId: aws.String("snap-ami")}},
		}}},
		LogGroups: []logstypes.LogGroup{
			{LogGroupName: aws.String("/big"), StoredBytes: aws.Int64(10 * gib)},
			{LogGroupName: aws.String("/kept"), StoredBytes: aws.Int64(100 * gib), RetentionInDays: aws.Int32(30)},
			{LogGroupName: aws.String("/small1"), StoredBytes: aws.Int64(gib / 2)},
			{LogGroupName: aws.String("/small2"), StoredBytes: aws.Int64(gib / 4)},
			{LogGroupName: aws.String("/ia"), StoredBytes: aws.Int64(gib), RetentionInDays: aws.Int32(7), LogGroupClass: logstypes.LogGroupClassInfrequentAccess},
		},
		LoadBalancers: []inventory.LoadBalancer{
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-idle/0f1e2d3c4b5a6978", Name: "lb-idle", Type: "application", TargetGroups: 1, Targets: 0},
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/net/lb-busy/0f1e2d3c4b5a6978", Name: "lb-busy", Type: "network", TargetGroups: 1, Targets: 3},
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-redirect/0f1e2d3c4b5a6978", Name: "lb-redirect", Type: "application", Responds: 1},
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-quiet/0f1e2d3c4b5a6978", Name: "lb-quiet", Type: "application", TargetGroups: 1, Targets: 2, Created: ago(60)},
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-new-quiet/0f1e2d3c4b5a6978", Name: "lb-new-quiet", Type: "application", TargetGroups: 1, Targets: 2, Created: ago(3)},
		},
		NatGateways: []ec2types.NatGateway{
			{NatGatewayId: aws.String("nat-1"), VpcId: aws.String("vpc-a"), State: ec2types.NatGatewayStateAvailable, CreateTime: ago(30)},
			{NatGatewayId: aws.String("nat-2"), VpcId: aws.String("vpc-b"), State: ec2types.NatGatewayStateAvailable, CreateTime: ago(30)},
			{NatGatewayId: aws.String("nat-3"), VpcId: aws.String("vpc-c"), State: ec2types.NatGatewayStateDeleted},
		},
		VpcEndpoints: []ec2types.VpcEndpoint{
			{VpcId: aws.String("vpc-b"), VpcEndpointType: ec2types.VpcEndpointTypeGateway, ServiceName: aws.String("com.amazonaws.eu-west-1.s3"), State: ec2types.State("available")},
			{VpcEndpointId: aws.String("vpce-s3-paid"), VpcId: aws.String("vpc-a"), VpcEndpointType: ec2types.VpcEndpointTypeInterface, ServiceName: aws.String("com.amazonaws.eu-west-1.s3"), State: ec2types.State("available"), SubnetIds: []string{"s1", "s2"}},
			{VpcEndpointId: aws.String("vpce-s3-onprem"), VpcId: aws.String("vpc-b"), VpcEndpointType: ec2types.VpcEndpointTypeInterface, ServiceName: aws.String("com.amazonaws.eu-west-1.s3"), State: ec2types.State("available"), SubnetIds: []string{"s1"}},
			{VpcEndpointId: aws.String("vpce-ecr-idle"), VpcId: aws.String("vpc-a"), VpcEndpointType: ec2types.VpcEndpointTypeInterface, ServiceName: aws.String("com.amazonaws.eu-west-1.ecr.dkr"), State: ec2types.State("available"), NetworkInterfaceIds: []string{"e1", "e2", "e3"}, CreationTimestamp: ago(60)},
			{VpcEndpointId: aws.String("vpce-sts-busy"), VpcId: aws.String("vpc-a"), VpcEndpointType: ec2types.VpcEndpointTypeInterface, ServiceName: aws.String("com.amazonaws.eu-west-1.sts"), State: ec2types.State("available"), SubnetIds: []string{"s1"}, CreationTimestamp: ago(60)},
			{VpcEndpointId: aws.String("vpce-gwlb-idle"), VpcId: aws.String("vpc-a"), VpcEndpointType: ec2types.VpcEndpointTypeGatewayLoadBalancer, ServiceName: aws.String("com.amazonaws.vpce.eu-west-1.vpce-svc-0abc"), State: ec2types.State("available"), SubnetIds: []string{"s1"}, CreationTimestamp: ago(60)},
		},
		Secrets: []smtypes.SecretListEntry{
			{Name: aws.String("never-read"), CreatedDate: ago(200)},
			{Name: aws.String("stale"), CreatedDate: ago(400), LastAccessedDate: ago(120)},
			{Name: aws.String("in-use"), CreatedDate: ago(400), LastAccessedDate: ago(10)},
			{Name: aws.String("rds!db-123"), CreatedDate: ago(400), OwningService: aws.String("rds")},
			{Name: aws.String("brand-new"), CreatedDate: ago(5)},
		},
		Repos: []inventory.Repo{
			{Name: "app", UntaggedCount: 3, UntaggedBytes: 5 * gib},
			{Name: "tidy", HasLifecycle: true},
			{Name: "leaky-policy", HasLifecycle: true, UntaggedCount: 1, UntaggedBytes: gib},
		},
		Functions: []lambdatypes.FunctionConfiguration{
			{FunctionName: aws.String("api"), Architectures: []lambdatypes.Architecture{"x86_64"}, MemorySize: aws.Int32(512), Timeout: aws.Int32(10),
				FunctionArn: aws.String("arn:aws:lambda:eu-west-1:123456789012:function:api")},
			{FunctionName: aws.String("worker"), Architectures: []lambdatypes.Architecture{"arm64"}, MemorySize: aws.Int32(1024), Timeout: aws.Int32(30),
				FunctionArn: aws.String("arn:aws:lambda:eu-west-1:123456789012:function:worker")},
			{FunctionName: aws.String("legacy")},
		},
		CacheClusters: []inventory.CacheCluster{
			{ID: "sessions-0001-001", ReplicationGroup: "sessions", Engine: "redis", Version: "5.0.6", NodeType: "cache.m5.large", Nodes: 1},
			{ID: "sessions-0001-002", ReplicationGroup: "sessions", Engine: "redis", Version: "5.0.6", NodeType: "cache.m5.large", Nodes: 1},
			{ID: "old-memcache", Engine: "memcached", Version: "1.6.22", NodeType: "cache.t2.medium", Nodes: 2},
			{ID: "v6-cache", Engine: "redis", Version: "6.2.6", NodeType: "cache.r6g.large", Nodes: 1},
			{ID: "fresh", Engine: "valkey", Version: "8.0", NodeType: "cache.t4g.small", Nodes: 1},
		},
		Tables: []inventory.Table{
			{Name: "orders", Provisioned: true, RCU: 100, WCU: 50, Created: ago(300)},
			{Name: "abandoned", Provisioned: true, RCU: 200, WCU: 200, Created: ago(300)},
			{Name: "scaled", Provisioned: true, RCU: 500, WCU: 500, AutoScaled: true, Created: ago(300)},
			{Name: "tiny", Provisioned: true, RCU: 5, WCU: 5, Created: ago(300)},
			{Name: "ondemand", Created: ago(300), Status: "ACTIVE", TableSizeBytes: 10 << 30, Bytes: 10 << 30},
			{Name: "archive", Status: "ACTIVE", Bytes: 500 << 30, Created: ago(300)}, // cold Standard: Standard-IA (Pro)
			{Name: "events", Status: "ACTIVE", Created: ago(300), Indexes: []string{"by-user", "by-day"},
				GSIs: []inventory.GSI{{Name: "by-user", Status: "ACTIVE", Bytes: 8 << 30}, {Name: "by-day", Status: "ACTIVE", Bytes: 20 << 30}}},
		},
		FastRestores: []inventory.FastRestore{
			{SnapshotID: "snap-fsr", AZ: "eu-west-1b", State: "enabled"},
			{SnapshotID: "snap-fsr", AZ: "eu-west-1a", State: "enabled"},
		},
		RecoveryPoints: []inventory.RecoveryPoint{
			{Vault: "Default", ResourceType: "EBS", ResourceArn: "arn:aws:ec2:eu-west-1:123456789012:volume/vol-gone", Bytes: 100 * gib, Created: ago(200)},
			{Vault: "Default", ResourceType: "EBS", ResourceArn: "arn:aws:ec2:eu-west-1:123456789012:volume/vol-gone", Bytes: 20 * gib, Created: ago(10)},
			{Vault: "Default", ResourceType: "EBS", ResourceArn: "arn:aws:ec2:eu-west-1:123456789012:volume/vol-unatt", Bytes: 100 * gib, Created: ago(10)}, // volume still exists
			{Vault: "Default", ResourceType: "RDS", ResourceArn: "arn:aws:rds:eu-west-1:123456789012:db:gone-db", Bytes: gib},                               // not checkable here
		},
		FileSystems: []inventory.FileSystem{
			{ID: "fs-orphan", Name: "old-share", MountTargets: 0, Standard: 100 * gib, Created: ago(300)},
			{ID: "fs-quiet", MountTargets: 2, Standard: 50 * gib, IA: 200 * gib, Created: ago(300)},
			{ID: "fs-busy", MountTargets: 2, Standard: 10 * gib, Created: ago(300)},
		},
		Streams: []inventory.Stream{
			{Name: "clicks", Mode: "PROVISIONED", Shards: 4, Created: ago(300)},
			{Name: "audit", Mode: "ON_DEMAND", Created: ago(300)},
			{Name: "orders", Mode: "PROVISIONED", Shards: 2, Created: ago(300)},
		},
		Buckets: []inventory.Bucket{
			{Name: "bare", Region: "eu-west-1", LifecycleRead: true},
			{Name: "has-rules", Region: "eu-west-1", HasLifecycle: true},
			{Name: "clean", Region: "eu-west-1", HasLifecycle: true, AbortsMPU: true},
		},
		Trails: []inventory.Trail{ // a member account's own trail beside the organization trail
			{Name: "org-trail", ARN: "arn:aws:cloudtrail:us-east-1:999999999999:trail/org-trail", HomeRegion: "us-east-1", MultiRegion: true, Organization: true, Logging: true, Management: "all"},
			{Name: "audit-copy", ARN: "arn:aws:cloudtrail:eu-west-1:123456789012:trail/audit-copy", HomeRegion: "eu-west-1", Logging: true, Management: "all"},
		},
		RestAPIMetricNames: map[string]int{"orders": 1, "shared": 2},
		RestStages: func() []inventory.RestStage {
			st := func(api, name, stage, size string, caching map[string]bool) inventory.RestStage {
				return inventory.RestStage{API: api, APIName: name, Stage: stage, CacheEnabled: true, CacheSize: size, CacheStatus: "AVAILABLE",
					MethodCaching: caching, Updated: ago(60), Tags: map[string]string{"team": "payments"}}
			}
			off, on := map[string]bool{"*/*": false, "~1pets/GET": false}, map[string]bool{"*/*": true}
			creating := st("api1", "orders", "creating", "0.5", off)
			creating.CacheStatus = "CREATE_IN_PROGRESS"
			fresh := st("api1", "orders", "new", "0.5", on)
			fresh.Updated = ago(2)
			return []inventory.RestStage{
				st("api1", "orders", "unused", "0.5", off),                   // api-cache-unused
				st("api1", "orders", "quiet", "6.1", on),                     // quiet: requests, no cache use
				st("api1", "orders", "nocalls", "1.6", on),                   // quiet, low: no requests at all
				st("api1", "orders", "busy", "0.5", on),                      // used
				st("api2", "shared", "shared", "0.5", on),                    // its name is another API's too
				st("api1", "orders", "nosettings", "0.5", map[string]bool{}), // what applies is unknown
				creating, fresh,
				{API: "api1", APIName: "orders", Stage: "nocache"},
			}
		}(),
		// What the paid plans load from CloudWatch and EKS.
		Usage: &inventory.Usage{
			Days:                  14,
			NatBytes:              map[string]float64{"nat-1": 2 << 20, "nat-2": 50 * float64(gib)},
			EndpointBytes:         map[string]float64{"vpce-ecr-idle": 0, "vpce-sts-busy": 1e9, "vpce-gwlb-idle": 0},
			APICacheUse:           map[string]float64{"api1/unused": 0, "api1/quiet": 0, "api1/nocalls": 0, "api1/busy": 40, "api2/shared": 0, "api1/new": 0},
			APIRequests:           map[string]float64{"api1/quiet": 5000, "api1/busy": 9000},
			EFSConnections:        map[string]float64{"fs-quiet": 0, "fs-busy": 900},
			StreamRecords:         map[string]float64{"clicks": 0, "audit": 0, "orders": 5e6},
			TableActivity:         map[string]float64{"orders": 1e6, "abandoned": 0, "scaled": 0, "tiny": 3},
			CustomMetrics:         map[string]int{"ContainerInsights": 12000, "CWAgent": 800, "MyApp": 40},
			CacheExtendedPrices:   map[string]inventory.ExtendedPrice{"cache.m5.large": {Year12: 0.138, Year3: 0.275}, "cache.r6g.large": {Year12: 0.166, Year3: 0.331}},
			LBTraffic:             map[string]float64{"arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-quiet/0f1e2d3c4b5a6978": 0, "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-new-quiet/0f1e2d3c4b5a6978": 0, "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/net/lb-busy/0f1e2d3c4b5a6978": 1e6, "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-idle/0f1e2d3c4b5a6978": 0},
			LogIngest:             map[string]float64{"/big": 200 * float64(gib), "/kept": float64(gib), "/ia": 500 * float64(gib)},
			S3Standard:            map[string]inventory.S3StandardUsage{"bare": {Bytes: 1 << 40, MonthlyUSD: 24.58}},
			S3StandardPriceSource: "eu-west-1 AWS Price List rates",

			PublicENIs: []ec2types.NetworkInterface{
				eni("eni-a", "amazon", "i-a", "subnet-1", "", ""),
				eni("eni-b", "amazon", "i-b", "subnet-1", "", ""),
				eni("eni-c", "amazon", "i-c", "subnet-2", "", ""),
				eni("eni-eip", "123456789012", "i-d", "subnet-1", "", ""),                     // Elastic IP on purpose
				eni("eni-rds", "amazon", "", "subnet-3", "amazon-rds", "RDSNetworkInterface"), // public database
				eni("eni-alb", "amazon", "", "subnet-1", "amazon-elb", "ELB app/web/abc"),     // needed by the ALB
				eni("eni-nat", "123456789012", "", "subnet-1", "", "Interface for NAT Gateway nat-1"),
			},
			Databases: []inventory.Database{
				{ID: "pg12", Engine: "postgres", Version: "12.19", Major: "12", Class: "db.r6g.large", StandardEnd: ago(600)},
				{ID: "old57", Engine: "mysql", Version: "5.7.44", Major: "5.7", Class: "db.m5.xlarge", StandardEnd: ago(800)},
				{ID: "aurora-1", Cluster: "c1", Engine: "aurora-postgresql", Version: "13.12", Major: "13", Class: "db.serverless", StandardEnd: ago(10)},
				{ID: "soon", Engine: "postgres", Version: "13.15", Major: "13", Class: "db.t3.medium", StandardEnd: ago(-60)},
				{ID: "optout", Engine: "mysql", Version: "5.7.44", Major: "5.7", Class: "db.m5.large", StandardEnd: ago(100), Enrollment: "open-source-rds-extended-support-disabled"},
				{ID: "fresh", Engine: "postgres", Version: "16.3", Major: "16", Class: "db.r6g.large", StandardEnd: ago(-900)},
				{ID: "unknown", Engine: "sqlserver-se", Version: "15.00", Class: "db.m5.large"},
			},
			CostHub: &inventory.CostHub{Enrolled: true, Recs: []inventory.Recommendation{
				{ID: "r1", Region: "eu-west-1", ResourceID: "i-big", ResourceType: "Ec2Instance", Action: "Rightsize", Current: "m5.2xlarge", Recommended: "m7g.xlarge", MonthlySavings: 120, Effort: "Medium", Restart: true, Rollback: true, Source: "ComputeOptimizer", LookbackDays: 14},
				{ID: "r2", Region: "eu-west-1", ResourceID: "vol-unatt", ResourceType: "EbsVolume", Action: "Delete", MonthlySavings: 10}, // our unattached-ebs has it
				{ID: "r3", Region: "eu-west-1", ResourceID: "i-a", ResourceType: "Ec2Instance", Action: "Stop", MonthlySavings: 30},       // also listed by public-ipv4? no: kept
				{ID: "r4", Region: "Global", ResourceType: "ComputeSavingsPlans", Action: "PurchaseSavingsPlans", Recommended: "Compute Savings Plan, 1 year, $0.50/h", MonthlySavings: 200},
				{ID: "r5", Region: "eu-west-1", ResourceID: "fn-1", ResourceType: "LambdaFunction", Action: "Rightsize", MonthlySavings: 0},
			}},
			ComputeOptimizer: &inventory.ComputeOptimizer{Enrolled: true, Recs: []inventory.ComputeOptimizerRec{
				{Kind: "Ec2Instance", Region: "eu-west-1", ResourceID: "i-co", ResourceARN: "arn:aws:ec2:eu-west-1:123456789012:instance/i-co",
					Current: "m5.2xlarge", Recommended: "m5.large", Finding: "Overprovisioned", FindingReasons: "CPUOverprovisioned",
					LookbackDays: 14, MonthlySavings: 55, PerformanceRisk: 0.5},
			}},
			Clusters: []inventory.EKSCluster{
				{Name: "old", Version: "1.29", VersionStatus: "extended-support", EndOfStandard: ago(100)},
				{Name: "new", Version: "1.33", VersionStatus: "standard-support"},
				{Name: "unknown", Version: "1.30"},
				{Name: "auto-upgraded", Version: "1.29", VersionStatus: "extended-support", EndOfStandard: ago(100), SupportType: "STANDARD"},
			},
		},
	})
	replication := inventory.DMSReplication{ARN: "arn:aws:dms:eu-west-1:123456789012:rep:retired", ID: "old-replication", Status: "stopped"}
	replication.LastStop = *ago(10)
	r.DMS = &inventory.DMS{Serverless: []inventory.DMSReplication{replication}}
	pcaARN := "arn:aws:acm-pca:eu-west-1:123456789012:certificate-authority/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	r.PCA = &inventory.PCA{
		CAs: []inventory.PrivateCA{{
			ARN: pcaARN, ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Status: "PENDING_CERTIFICATE",
			Type: "ROOT", UsageMode: "GENERAL_PURPOSE", CommonName: "fixture-pca", Created: *ago(40), PolicyRead: true,
		}},
		AnchorsRead: true, Anchored: map[string]bool{}, ACMNamed: map[string]bool{},
	}
	r.WorkSpaces = &inventory.WorkSpaces{Items: []inventory.WorkSpace{{
		ID: "ws-unused", DirectoryID: "d-unused", UserName: "jane.doe", State: "AVAILABLE",
		RunningMode: "ALWAYS_ON", ComputeType: "STANDARD", OS: "WINDOWS_10", RootGiB: 80, UserGiB: 50,
		StatusRead: true, ConnectionState: "DISCONNECTED",
		ConnectionChecked: now, LastSignIn: now.AddDate(0, 0, -40),
	}}}
	addMigrationUsageFixture(r)
	addMQFixture(r)
	addAIUsageFixture(r)
	addGlueFixture(r)
	addIdleServicesFixture(r)
	addShieldFixture(r)
	addCloudFrontSSLFixture(r)
	addAppStreamFixture(r)
	addOSIFixture(r)
	addOpenSearchSizingFixture(r)
	addRedshiftSizingFixture(r)
	addFSxUsageFixture(r)
	addDXUsageFixture(r)
	addLightsailUsageFixture(r)
	addDBClusterUsageFixture(r)
	addAuroraStorageFixture(r)
	addECSIdleFixture(r)
	addECSCapacityFixture(r)
	addCanaryFixture(r)
	addRightsizeFixture(r)
	addRDSResizeFixture(r)
	addEBSPerformanceFixture(r)
	addEMRAutoTermFixture(r)
	addServiceUsageFixture(r)
	addRoundPFixture(r)
	addEFSProvisionedThroughputFixture(r)
	addMultiAZBurstFixture(r)
	addWorkspaceBillingModeFixture(r)
	addDDBAutoscaleFloorFixture(r)
	return r
}

func byResource(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.Resource] = f
	}
	return m
}

func wantUSD(t *testing.T, f Finding, usd float64) {
	t.Helper()
	if !f.Priced || math.Abs(f.MonthlyUSD-usd) > 0.0001 {
		t.Errorf("%s: priced=%v $%.4f, want priced $%.4f", f.Resource, f.Priced, f.MonthlyUSD, usd)
	}
}

func opts() Options { return DefaultOptions(now) }

func teamOpts() Options { o := opts(); o.Plan = license.PlanTeam; return o }

func TestUnattachedVolumes(t *testing.T) {
	got := UnattachedVolumes(fixture(), opts())
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Resource != "vol-unatt (old-data)" {
		t.Errorf("resource = %q", f.Resource)
	}
	wantUSD(t, f, 10)
	if len(f.Fix) != 2 || !strings.Contains(f.Fix[0], "create-snapshot") || !strings.Contains(f.Fix[1], "delete-volume --region eu-west-1 --volume-id vol-unatt") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestGP2ToGP3(t *testing.T) {
	got := byResource(GP2ToGP3(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("got %v, want vol-run and vol-stopnew only", got)
	}
	wantUSD(t, got["vol-run"], 2)       // 100 GiB: $10 -> $8
	wantUSD(t, got["vol-stopnew"], 0.4) // instance stopped 1 day: not reported as stopped, so still a gp3 candidate
	if _, ok := got["vol-unatt (old-data)"]; ok {
		t.Error("unattached volume counted twice")
	}
	if _, ok := got["vol-stop"]; ok {
		t.Error("volume of a long-stopped instance counted twice")
	}
	if fix := got["vol-run"].Fix[0]; !strings.Contains(fix, "--volume-type gp3 --iops 3000 --throughput 125") {
		t.Errorf("fix = %q", fix)
	}
}

func TestStoppedInstances(t *testing.T) {
	got := StoppedInstances(fixture(), opts())
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1 (i-stop): %+v", len(got), got)
	}
	f := got[0]
	if f.Resource != "i-stop" || !strings.Contains(f.Detail, "2026-10-01 (47 days ago)") {
		t.Errorf("finding = %+v", f)
	}
	wantUSD(t, f, 5)
}

func TestStoppedUnknownDateCounts(t *testing.T) {
	r := fixture()
	r.Instances = []ec2types.Instance{inst("i-x", "m5.large", ec2types.InstanceStateNameStopped, "", "vol-stop")}
	if got := StoppedInstances(r, opts()); len(got) != 1 {
		t.Fatalf("stopped instance with no stop date should be reported, got %v", got)
	}
}

func TestStoppedSinceUTC(t *testing.T) {
	in := inst("i-x", "m5.large", ec2types.InstanceStateNameStopped, "User initiated (2026-10-01 10:00:00 UTC)")
	if got := stoppedSince(in); got == nil || got.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("stoppedSince = %v", got)
	}
}

func TestIdleIPsEmptyStrings(t *testing.T) {
	// Some AWS-compatible APIs return "" instead of omitting unset fields.
	r := &inventory.Region{Name: "x", Addresses: []ec2types.Address{
		{AllocationId: aws.String("eipalloc-1"), PublicIp: aws.String("203.0.113.9"), NetworkInterfaceId: aws.String(""), InstanceId: aws.String("")},
		{AllocationId: aws.String("eipalloc-2"), PublicIp: aws.String("203.0.113.10"), AssociationId: aws.String("a"), NetworkInterfaceId: aws.String("eni-1"), InstanceId: aws.String("")},
	}}
	got := IdleIPs(r, opts())
	if len(got) != 1 || got[0].Resource != "203.0.113.9" {
		t.Fatalf("got %+v, want only the unattached 203.0.113.9", got)
	}
}

func TestIdleIPs(t *testing.T) {
	got := byResource(IdleIPs(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("got %v, want the free EIP and the one on the stopped instance", got)
	}
	wantUSD(t, got["203.0.113.1"], 3.65)
	if !strings.Contains(got["203.0.113.3"].Detail, "stopped instance i-stop") {
		t.Errorf("detail = %q", got["203.0.113.3"].Detail)
	}
	if fix := got["203.0.113.1"].Fix[0]; fix != "aws ec2 release-address --region eu-west-1 --allocation-id eipalloc-free" {
		t.Errorf("fix = %q", fix)
	}
}

func TestOldSnapshots(t *testing.T) {
	got := OldSnapshots(fixture(), opts())
	m := byResource(got)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(got), got)
	}
	gone := m["2 snapshot(s) of vol-gone"]
	wantUSD(t, gone, 6*0.05) // whole lineage old: priced at the largest full size
	if !strings.Contains(gone.Detail, "source volume vol-gone is deleted") || len(gone.Fix) != 2 ||
		!strings.Contains(gone.Fix[0], "snap-a") || !strings.Contains(gone.Fix[1], "snap-b") {
		t.Errorf("vol-gone finding = %+v", gone)
	}
	run := m["1 snapshot(s) of vol-run"]
	if run.Priced || !strings.Contains(run.Detail, "1 newer snapshot(s)") || !strings.Contains(run.Detail, "still exists") {
		t.Errorf("vol-run finding = %+v", run)
	}
	for _, f := range got {
		for _, fix := range f.Fix {
			for _, never := range []string{"snap-ami", "snap-backup", "snap-dlm", "snap-pending", "snap-d"} {
				if strings.Contains(fix, never+" ") || strings.HasSuffix(fix, never) {
					t.Errorf("%s must not be deleted: %q", never, fix)
				}
			}
		}
	}
}

func TestOldSnapshotsFallsBackToVolumeSize(t *testing.T) {
	r := &inventory.Region{Name: "us-east-1", Snapshots: []ec2types.Snapshot{
		snap("snap-x", "vol-x", 365, 20, 0, ec2types.SnapshotStateCompleted),
	}}
	got := OldSnapshots(r, opts())
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	wantUSD(t, got[0], 20*0.05)
}

func TestLogRetention(t *testing.T) {
	got := byResource(LogRetention(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("got %v, want /big and the grouped small ones", got)
	}
	wantUSD(t, got["/big"], 10*0.03)
	wantUSD(t, got["2 smaller log group(s)"], 0.75*0.03)
	if !strings.Contains(got["/big"].Detail, "priced at the Standard storage rate") {
		t.Errorf("/big has no class: Detail should note Standard pricing: %q", got["/big"].Detail)
	}
	if _, ok := got["/kept"]; ok {
		t.Error("log group with retention reported")
	}
	if fix := got["/big"].Fix[0]; fix != "aws logs put-retention-policy --region eu-west-1 --log-group-name '/big' --retention-in-days 30" {
		t.Errorf("fix = %q", fix)
	}
}

func TestLogRetentionByClass(t *testing.T) {
	r := &inventory.Region{Name: "us-east-1", LogGroups: []logstypes.LogGroup{
		{LogGroupName: aws.String("/ia-keep"), StoredBytes: aws.Int64(20 * gib), LogGroupClass: logstypes.LogGroupClassInfrequentAccess},
		{LogGroupName: aws.String("/archive"), StoredBytes: aws.Int64(10 * gib), LogGroupClass: "ARCHIVE_INSTANT_ACCESS"},
		{LogGroupName: aws.String("/std"), StoredBytes: aws.Int64(5 * gib), LogGroupClass: logstypes.LogGroupClassStandard},
	}}
	got := byResource(LogRetention(r, opts()))
	wantUSD(t, got["/ia-keep"], 20*0.018)
	wantUSD(t, got["/archive"], 10*0.006)
	wantUSD(t, got["/std"], 5*0.03)
	if strings.Contains(got["/std"].Detail, "priced at the Standard storage rate") {
		t.Errorf("known Standard class must not claim the class was missing: %q", got["/std"].Detail)
	}
}

func TestIdleLoadBalancers(t *testing.T) {
	got := byResource(IdleLoadBalancers(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	wantUSD(t, got["lb-idle"], 16.425)
	if f := got["lb-redirect"]; !strings.Contains(f.Detail, "redirect or return a fixed response") || f.Confidence != "low" {
		t.Errorf("lb-redirect: %+v", f)
	}
	if _, ok := got["lb-busy"]; ok {
		t.Error("load balancer with targets reported")
	}
}

func TestNATWithoutS3Endpoint(t *testing.T) {
	got := NATWithoutS3Endpoint(fixture(), opts())
	if len(got) != 1 || got[0].Resource != "vpc-a" || got[0].Priced {
		t.Fatalf("got %+v, want unpriced vpc-a only (vpc-b has the gateway endpoint, vpc-c's NAT is deleted, an interface endpoint doesn't count)", got)
	}
}

func TestOldInstanceTypes(t *testing.T) {
	got := OldInstanceTypes(fixture(), opts())
	if len(got) != 1 || got[0].Resource != "i-run" || got[0].Priced || !strings.Contains(got[0].Detail, "t2.micro") {
		t.Fatalf("got %+v, want only the running t2.micro", got)
	}
}

var addLightsailUsageFixture = func(*inventory.Region) {}
var addAIUsageFixture = func(*inventory.Region) {}
var addGlueFixture = func(*inventory.Region) {}
var addMQFixture = func(*inventory.Region) {}
var addIdleServicesFixture = func(*inventory.Region) {}
var addShieldFixture = func(*inventory.Region) {}
var addCloudFrontSSLFixture = func(*inventory.Region) {}

var addAppStreamFixture = func(*inventory.Region) {}
var addOSIFixture = func(*inventory.Region) {}
var addCanaryFixture = func(*inventory.Region) {}
var addECSIdleFixture = func(*inventory.Region) {}
var addECSCapacityFixture = func(*inventory.Region) {}
var addRightsizeFixture = func(*inventory.Region) {}
var addRDSResizeFixture = func(*inventory.Region) {}
var addAuroraStorageFixture = func(*inventory.Region) {}
var addEBSPerformanceFixture = func(*inventory.Region) {}
var addEMRAutoTermFixture = func(*inventory.Region) {}

// addCoverageFixture adds resources only the every-check coverage tests use:
// a resource that would change other checks' exact expectations on fixture().
var addCoverageFixture = func(*inventory.Region) {}

func coverageFixture() *inventory.Region {
	r := fixture()
	addCoverageFixture(r)
	addSpotFixture(r)
	addValkeyFixture(r)
	if BuildHasPro {
		if r.Usage == nil {
			r.Usage = &inventory.Usage{}
		}
		r.Usage.CostExplorer = &inventory.CostExplorerUtilization{
			WindowStart: "2026-10-28", WindowEnd: "2026-11-27",
			Reservations: []inventory.RIUtilizationGroup{{
				Service: "EC2", SubscriptionID: "sub-cov",
				Attributes:  map[string]string{"InstanceType": "m5.large", "NumberOfInstances": "1"},
				Utilization: inventory.ReservationUtilization{UtilizationPercent: "50", RICostForUnusedHours: "100"},
			}},
		}
	}
	return r
}

var addValkeyFixture = func(*inventory.Region) {}

var addSpotFixture = func(*inventory.Region) {}
var addOpenSearchSizingFixture = func(*inventory.Region) {}
var addServiceUsageFixture = func(*inventory.Region) {}

var addRoundPFixture = func(*inventory.Region) {}

var addEFSProvisionedThroughputFixture = func(*inventory.Region) {}

var addMultiAZBurstFixture = func(*inventory.Region) {}
var addWorkspaceBillingModeFixture = func(*inventory.Region) {}

var addRedshiftSizingFixture = func(*inventory.Region) {}

var addDDBAutoscaleFloorFixture = func(*inventory.Region) {}

func TestRunCoversEveryCheck(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Run(coverageFixture(), teamOpts()) {
		seen[f.Check] = true
	}
	for _, c := range All {
		if c.Run != nil && !seen[c.ID] {
			t.Errorf("check %s produced nothing on the fixture", c.ID)
		}
	}
}

func TestEmptyRegion(t *testing.T) {
	if got := Run(&inventory.Region{Name: "x"}, opts()); len(got) != 0 {
		t.Fatalf("empty region produced %v", got)
	}
}

func TestPlanGating(t *testing.T) {
	paid := map[string]bool{}
	for _, c := range All {
		if c.Plan != license.PlanFree {
			paid[c.ID] = true
		}
	}
	for _, f := range Run(fixture(), opts()) {
		if paid[f.Check] {
			t.Errorf("free plan ran paid check %s", f.Check)
		}
	}
	if got := Count(license.PlanPro) + Count(license.PlanTeam); got != len(paid) {
		t.Errorf("Count: Pro+Team = %d, paid checks = %d", got, len(paid))
	}
	// Without usage data the paid checks stay quiet instead of guessing.
	r := fixture()
	r.Usage = nil
	for _, f := range Run(r, teamOpts()) {
		if paid[f.Check] {
			t.Errorf("paid check %s ran without usage data", f.Check)
		}
	}
}

func eni(id, ipOwner, instance, subnet, requester, desc string) ec2types.NetworkInterface {
	n := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String(id), SubnetId: aws.String(subnet),
		Association: &ec2types.NetworkInterfaceAssociation{PublicIp: aws.String("203.0.113." + id[4:5]), IpOwnerId: aws.String(ipOwner)},
	}
	if instance != "" {
		n.Attachment = &ec2types.NetworkInterfaceAttachment{InstanceId: aws.String(instance)}
	}
	if requester != "" {
		n.RequesterId = aws.String(requester)
	}
	if desc != "" {
		n.Description = aws.String(desc)
	}
	return n
}

func piops(id, typ string, size, iops int32) ec2types.Volume {
	v := vol(id, ec2types.VolumeType(typ), size, ec2types.VolumeStateInUse)
	v.Iops = aws.Int32(iops)
	return v
}

func TestPIOPSToGP3(t *testing.T) {
	got := byResource(PIOPSToGP3(fixture(), opts()))
	if len(got) != 4 {
		t.Fatalf("want vol-io1, vol-io2-fast, vol-io1-big, vol-io1-outpost-ok; not the dense, Multi-Attach or over-Outpost ones: %v", got)
	}
	f := got["vol-io1"]
	// io1: 500*0.125 + 8000*0.065 = 582.50; io1 at 8,000 IOPS delivers at most
	// 500 MiB/s, so gp3: 500*0.08 + 5000*0.005 + 375*0.04 = 80.
	wantUSD(t, f, 502.50)
	if !strings.Contains(f.Fix[0], "--volume-type gp3 --iops 8000 --throughput 500") || f.Confidence != "" {
		t.Errorf("fix = %v, confidence %q", f.Fix, f.Confidence)
	}
	// io2 at 20,000 IOPS can deliver 4,000 MiB/s; gp3 stops at 2,000:
	// io2 100*0.125 + 20000*0.065 = 1312.50; gp3 100*0.08 + 17000*0.005 + 1875*0.04 = 168.
	f = got["vol-io2-fast"]
	wantUSD(t, f, 1144.50)
	if f.Confidence != "low" || !strings.Contains(f.Detail, "gp3 tops out at 2000 MiB/s here, and this io2 volume can deliver up to 4000 MiB/s") ||
		!strings.Contains(f.Why, "latency-sensitive database") {
		t.Errorf("over 16,000 IOPS and over gp3's throughput: low, and says why: %+v", f)
	}
	// The report's own command asks first too: a comment, after both reasons.
	if len(f.Fix) != 2 || !strings.HasPrefix(f.Fix[0], "# check the workload first: it has 20000 IOPS") ||
		!strings.Contains(f.Fix[0], "; and gp3 gets 2000 MiB/s here, and it can deliver 4000 MiB/s") ||
		f.Fix[1] != "# aws ec2 modify-volume --region eu-west-1 --volume-id vol-io2-fast --volume-type gp3 --iops 20000 --throughput 2000" {
		t.Errorf("a trade-off: the command is a comment, after the reasons: %q", f.Fix)
	}
	// io1 at 48,000 IOPS delivers 750 MiB/s: io1 1000*0.125 + 48000*0.065 = 3245;
	// gp3 1000*0.08 + 45000*0.005 + 625*0.04 = 330; io2 125 + 32000*0.065 + 16000*0.0455 = 2933.
	f = got["vol-io1-big"]
	wantUSD(t, f, 2915)
	if f.Confidence != "low" || !strings.Contains(f.Detail, "or io2, as fast or faster: $2933 a month ($312 less), changed online") ||
		!strings.HasPrefix(f.Fix[0], "# check the workload first: it has 48000 IOPS") || strings.Contains(f.Fix[0], "MiB/s") ||
		f.Fix[1] != "# aws ec2 modify-volume --region eu-west-1 --volume-id vol-io1-big --volume-type gp3 --iops 48000 --throughput 750" {
		t.Errorf("io1 over 32,000 IOPS: gp3 (a comment: latency), or io2 for less: %+v", f)
	}
	// On an Outpost: 12,000 IOPS fits, throughput capped at 500 (io1's own limit, under the Outpost's 1,000).
	if f := got["vol-io1-outpost-ok"]; !strings.Contains(f.Fix[0], "--iops 12000 --throughput 500") {
		t.Errorf("Outpost volume: %+v", f)
	}
}

func TestIO1ToIO2(t *testing.T) {
	got := byResource(IO1ToIO2(fixture(), opts()))
	if len(got) != 1 {
		t.Fatalf("want only the Multi-Attach io1 volume off Outposts: %v", got)
	}
	f := got["vol-io1-ma"]
	// io1 1000*0.125 + 40000*0.065 = 2725; io2 125 + 32000*0.065 + 8000*0.0455 = 2569.
	wantUSD(t, f, 156)
	if f.Fix[0] != "aws ec2 modify-volume --region eu-west-1 --volume-id vol-io1-ma --volume-type io2 --iops 40000" || f.Facts["to_type"] != "io2" {
		t.Errorf("fix %v, facts %v", f.Fix, f.Facts)
	}
	// Under 32,000 IOPS io1 and io2 cost the same: nothing to save.
	r := &inventory.Region{Name: "eu-west-1", Volumes: []ec2types.Volume{func() ec2types.Volume {
		v := piops("vol-io1-ma-small", "io1", 500, 20000)
		v.MultiAttachEnabled = aws.Bool(true)
		return v
	}()}}
	if got := IO1ToIO2(r, opts()); len(got) != 0 {
		t.Errorf("same price under 32,000 IOPS: %+v", got)
	}
}

func TestUnusedSecrets(t *testing.T) {
	got := byResource(UnusedSecrets(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("want never-read and stale, got %v", got)
	}
	wantUSD(t, got["never-read"], 0.40)
	if !strings.Contains(got["stale"].Detail, "last read") || !strings.Contains(got["never-read"].Detail, "never read") {
		t.Errorf("details: %q / %q", got["stale"].Detail, got["never-read"].Detail)
	}
	if !strings.Contains(strings.Join(got["never-read"].Fix, " "), "--recovery-window-in-days 30") {
		t.Error("deleting a secret must keep the recovery window")
	}
}

func TestECRCleanup(t *testing.T) {
	got := byResource(ECRCleanup(fixture(), opts()))
	if len(got) != 2 {
		t.Fatalf("want app and leaky-policy, got %v", got)
	}
	wantUSD(t, got["app"], 0.5)
	wantUSD(t, got["leaky-policy"], 0.1)
	if !strings.Contains(got["app"].Fix[0], "put-lifecycle-policy") || strings.Contains(got["leaky-policy"].Fix[0], "put-lifecycle-policy") {
		t.Error("only a repo with no policy gets put-lifecycle-policy (it would replace an existing one)")
	}
}

func TestLambdaOnX86(t *testing.T) {
	got := LambdaOnX86(fixture(), opts())
	if len(got) != 1 || got[0].Resource != "2 function(s) on x86_64" || !strings.Contains(got[0].Detail, "api, legacy") {
		t.Fatalf("got %+v", got)
	}
}

func TestS3NoAbortMPU(t *testing.T) {
	got := byResource(S3NoAbortMPU(fixture(), opts()))
	bare, rules := got["1 bucket(s) with no lifecycle rules"], got["1 bucket(s) whose rules don't abort failed uploads"]
	if len(got) != 2 || bare.Check == "" || rules.Check == "" {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(strings.Join(bare.Fix, " "), "put-bucket-lifecycle-configuration") {
		t.Error("a bucket with no rules gets the put command")
	}
	if strings.Contains(strings.Join(rules.Fix, " "), "aws s3api put-bucket-lifecycle-configuration") {
		t.Error("a bucket WITH rules must not get a put command: it would wipe them")
	}
}

func TestPaidS3Endpoint(t *testing.T) {
	got := byResource(PaidS3Endpoint(fixture(), opts()))
	if len(got) != 1 {
		t.Fatalf("want vpce-s3-paid only (vpc-b also has the free gateway, so its interface one is deliberate), got %v", got)
	}
	wantUSD(t, got["vpce-s3-paid"], 2*7.30)
}

func TestOldCacheNodes(t *testing.T) {
	got := byResource(OldCacheNodes(fixture(), opts()))
	if len(got) != 1 || !strings.Contains(got["old-memcache"].Detail, "2 x cache.t2.medium") {
		t.Fatalf("got %v", got)
	}
}

func TestDynamoDBNoAutoScaling(t *testing.T) {
	free := fixture()
	free.Usage = nil // a free run has no usage data
	got := byResource(DynamoDBNoAutoScaling(free, opts()))
	// free plan: orders + abandoned; scaled has auto scaling, tiny costs under
	// $5, ondemand isn't provisioned.
	if len(got) != 2 || got["orders"].Priced || !strings.Contains(got["orders"].Detail, "$33.21") {
		t.Fatalf("free: %v", got)
	}
}

func TestPricingTiers(t *testing.T) {
	if got := pricing.DynamoDBProvisionedMonthly(100, 50); math.Abs(got-33.215) > 0.001 {
		t.Errorf("DynamoDBProvisionedMonthly = %v", got)
	}
}

func TestFastSnapshotRestore(t *testing.T) {
	got := FastSnapshotRestore(fixture(), opts())
	if len(got) != 1 || !strings.Contains(got[0].Fix[0], "--availability-zones eu-west-1a eu-west-1b") {
		t.Fatalf("got %+v", got)
	}
	wantUSD(t, got[0], 2*0.75*730)
}

func TestBackupOrphans(t *testing.T) {
	got := byResource(BackupOrphans(fixture(), opts()))
	f, ok := got["volume/vol-gone (deleted)"]
	if len(got) != 1 || !ok || f.Priced {
		t.Fatalf("want only vol-gone, unpriced: %v", got)
	}
	if !strings.Contains(f.Detail, "2 recovery point(s)") || !strings.Contains(f.Detail, "120 GB") || !strings.Contains(f.Detail, "$6") {
		t.Errorf("detail = %s", f.Detail)
	}
}

func TestEFSNoMountTargets(t *testing.T) {
	got := byResource(EFSNoMountTargets(fixture(), opts()))
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	wantUSD(t, got["fs-orphan (old-share)"], 100*0.30)
}

// Fix bundles are built from Facts, so the checks must fill them with the
// exact identifiers and the state the scan saw.
func TestFactsForFixScripts(t *testing.T) {
	r := fixture()
	r.Volumes[3].AvailabilityZone = aws.String("eu-west-1a")
	got := map[string]map[string]string{}
	for _, f := range Run(r, opts()) {
		got[f.Check+" "+f.Key] = f.Facts
	}
	want := map[string]map[string]string{
		"unattached-ebs vol-unatt":                 {"volume": "vol-unatt", "az": "eu-west-1a", "type": "gp2", "size_gib": "100"},
		"stopped-instance i-stop":                  {"instance": "i-stop", "type": "m5.large", "volumes": "vol-stop"},
		"idle-ip 203.0.113.1":                      {"allocation": "eipalloc-free", "ip": "203.0.113.1"},
		"idle-ip 203.0.113.3":                      {"allocation": "eipalloc-stop", "ip": "203.0.113.3", "instance": "i-stop"},
		"old-snapshots snapshots-of-vol-gone":      {"volume": "vol-gone", "snapshots": "snap-a snap-b"},
		"gp2-to-gp3 vol-run":                       {"volume": "vol-run", "from_type": "gp2", "size_gib": "100", "to_iops": "3000", "to_throughput": "125"},
		"piops-to-gp3 vol-io1":                     {"volume": "vol-io1", "from_type": "io1", "from_iops": "8000", "from_throughput": "500", "size_gib": "500", "to_iops": "8000", "to_throughput": "500"},
		"piops-to-gp3 vol-io2-fast":                {"volume": "vol-io2-fast", "from_type": "io2", "from_iops": "20000", "from_throughput": "4000", "size_gib": "100", "to_iops": "20000", "to_throughput": "2000"},
		"io1-to-io2 vol-io1-ma":                    {"volume": "vol-io1-ma", "from_type": "io1", "from_iops": "40000", "size_gib": "1000", "to_type": "io2", "to_iops": "40000"},
		"log-retention /big":                       {"log_group": "/big", "days": "30"},
		"idle-load-balancer lb-idle":               {"lb_arn": "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/lb-idle/0f1e2d3c4b5a6978", "lb_name": "lb-idle", "lb_type": "application"},
		"nat-no-s3-endpoint vpc-a":                 {"vpc": "vpc-a", "service": "com.amazonaws.eu-west-1.s3"},
		"unused-secret stale":                      {"name": "stale", "last_read": ago(120).UTC().Format("2006-01-02")},
		"unused-secret never-read":                 {"name": "never-read", "last_read": "never"},
		"ecr-cleanup app":                          {"repository": "app", "has_lifecycle": "false"},
		"s3-abort-mpu buckets-without-rules":       {"buckets": "bare", "has_rules": "false"},
		"s3-abort-mpu buckets-rules-without-abort": {"buckets": "has-rules", "has_rules": "true"},
		"paid-s3-endpoint vpce-s3-paid":            {"endpoint": "vpce-s3-paid", "vpc": "vpc-a", "service": "com.amazonaws.eu-west-1.s3"},
		"dynamodb-no-autoscaling orders":           {"table": "orders"},
		"fast-snapshot-restore snap-fsr":           {"snapshot": "snap-fsr", "azs": "eu-west-1a eu-west-1b"},
		"efs-no-mount-targets fs-orphan":           {"file_system": "fs-orphan"},
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("no finding %q", k)
			continue
		}
		for name, v := range w {
			if g[name] != v {
				t.Errorf("%s: facts[%s] = %q, want %q (all: %v)", k, name, g[name], v, g)
			}
		}
	}
	for k, g := range got {
		for name, v := range g {
			if v == "" || strings.ContainsAny(v, "\n'\"") {
				t.Errorf("%s: fact %s = %q: facts must be plain non-empty values", k, name, v)
			}
		}
	}
}

// An unattached Kubernetes PersistentVolume is not the same as a forgotten
// disk: its pod may only be scaled to zero.
func TestUnattachedKubernetesVolume(t *testing.T) {
	r := &inventory.Region{Name: "eu-west-1", Volumes: []ec2types.Volume{func() ec2types.Volume {
		v := vol("vol-pv", ec2types.VolumeTypeGp3, 50, ec2types.VolumeStateAvailable)
		v.Tags = []ec2types.Tag{
			{Key: aws.String("kubernetes.io/created-for/pv/name"), Value: aws.String("pvc-1234")},
			{Key: aws.String("kubernetes.io/created-for/pvc/name"), Value: aws.String("data-db-0")},
			{Key: aws.String("kubernetes.io/created-for/pvc/namespace"), Value: aws.String("prod")},
		}
		return v
	}()}}
	got := UnattachedVolumes(r, opts())
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	f := withDefaults(got[0])
	if f.Confidence != "medium" || f.Facts["kubernetes_pv"] != "pvc-1234" || !strings.Contains(strings.Join(f.Evidence, "|"), "claimed by PVC prod/data-db-0") {
		t.Fatalf("%+v", f)
	}
}

func TestAPICacheUnused(t *testing.T) {
	got := byResource(APICacheUnused(fixture(), opts()))
	if len(got) != 1 {
		t.Fatalf("want only the stage with caching off everywhere (not one without settings, nor one being created): %v", got)
	}
	f := got["api1/stages/unused (orders)"]
	wantUSD(t, f, 0.02*730)
	for _, l := range f.Fix {
		if !strings.HasPrefix(l, "#") {
			t.Errorf("turning a cache off is a person's call: comments only: %q", f.Fix)
		}
	}
	if f.Facts["arn"] != "arn:aws:apigateway:eu-west-1::/restapis/api1/stages/unused" || f.Facts["cache_size_gb"] != "0.5" {
		t.Errorf("facts %v", f.Facts)
	}
}
