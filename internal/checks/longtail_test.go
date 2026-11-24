package checks

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func amiAt(id, name string, ageDays int, mappings ...ec2types.BlockDeviceMapping) ec2types.Image {
	return ec2types.Image{ImageId: aws.String(id), Name: aws.String(name), State: ec2types.ImageStateAvailable,
		CreationDate: aws.String(now.AddDate(0, 0, -ageDays).Format(time.RFC3339)), BlockDeviceMappings: mappings}
}

func ebsMap(snapID string, gib int32) ec2types.BlockDeviceMapping {
	return ec2types.BlockDeviceMapping{Ebs: &ec2types.EbsBlockDevice{SnapshotId: aws.String(snapID), VolumeSize: aws.Int32(gib)}}
}

// withLongTail adds what the long-tail checks look at to the shared fixture.
func withLongTail(r *inventory.Region) *inventory.Region {
	public := amiAt("ami-public", "shared-base", 300, ebsMap("snap-pub", 8))
	public.Public = aws.Bool(true)
	r.Images = append(r.Images,
		amiAt("ami-old-unused", "web-2025", 200, ebsMap("snap-ami-a", 8), ebsMap("snap-ami-b", 20)),
		amiAt("ami-running", "web-live", 400, ebsMap("snap-run", 8)),
		amiAt("ami-in-template", "asg-base", 400, ebsMap("snap-tpl", 8)),
		public,
		amiAt("ami-backup", "AwsBackup_job-1234", 400, ebsMap("snap-bk", 8)),
		amiAt("ami-young", "web-new", 10, ebsMap("snap-young", 8)),
	)
	archived := snap("snap-ami-b", "vol-ami-src", 200, 20, 0, ec2types.SnapshotStateCompleted)
	archived.StorageTier = ec2types.StorageTierArchive
	r.Snapshots = append(r.Snapshots, snap("snap-ami-a", "vol-ami-src", 200, 8, 5, ec2types.SnapshotStateCompleted), archived)
	r.Instances[0].ImageId = aws.String("ami-running")
	r.ImageRefs = map[string]bool{"ami-in-template": true}
	r.Hosts = []inventory.Host{
		{ID: "h-idle", Family: "m5", AZ: "eu-west-1a", State: "available", Allocated: ago(40)},
		{ID: "h-busy", Family: "m5", AZ: "eu-west-1a", State: "available", Instances: 3, Allocated: ago(40)},
		{ID: "h-reserved", Family: "c5", AZ: "eu-west-1b", State: "available", Reservation: "hr-0abc", Allocated: ago(400)},
		{ID: "h-failed", Family: "r5", AZ: "eu-west-1b", State: "permanent-failure", Allocated: ago(40)},
	}
	r.Keys = []inventory.KMSKey{
		{ID: "key-disabled", ARN: "arn:aws:kms:eu-west-1:123456789012:key/key-disabled", Alias: "alias/old-app", State: "Disabled", Created: ago(500)},
		{ID: "key-rotated", ARN: "arn:aws:kms:eu-west-1:123456789012:key/key-rotated", State: "Disabled", Rotations: 5, Created: ago(2000)},
		{ID: "key-import", ARN: "arn:aws:kms:eu-west-1:123456789012:key/key-import", State: "PendingImport", Created: ago(90)},
		{ID: "key-enabled", ARN: "arn:aws:kms:eu-west-1:123456789012:key/key-enabled", State: "Enabled", Created: ago(90)},
		{ID: "key-deleting", ARN: "arn:aws:kms:eu-west-1:123456789012:key/key-deleting", State: "PendingDeletion", Created: ago(90)},
	}
	r.DBSnapshots = []inventory.DBSnapshot{
		{ID: "olddb-final", Source: "olddb", Engine: "postgres", GiB: 200, Created: ago(150)},
		{ID: "olddb-2025", Source: "olddb", Engine: "postgres", GiB: 100, Created: ago(200)},
		{ID: "oldcluster-final", Source: "oldcluster", Engine: "aurora-mysql", Cluster: true, GiB: 500, Created: ago(120)},
		{ID: "mixed-old", Source: "mixed", Engine: "mysql", GiB: 50, Created: ago(120)},
		{ID: "mixed-new", Source: "mixed", Engine: "mysql", GiB: 50, Created: ago(10)},
		{ID: "live-weekly", Source: "livedb", Engine: "mysql", GiB: 80, Created: ago(300), SourceExists: true},
		{ID: "gone2-final", Source: "gone2", Engine: "mariadb", GiB: 30, Created: ago(100), FreeAllowance: true},
		{ID: "dr-copy-1", Source: "prod-db", Engine: "postgres", GiB: 400, Created: ago(200), Copy: true, CopyFrom: "us-east-1"},
		{ID: "dr-copy-2", Source: "prod-db", Engine: "postgres", GiB: 400, Created: ago(150), Copy: true, CopyFrom: "us-east-1"},
		{ID: "maz-final", Source: "maz-cluster", Engine: "postgres", Cluster: true, GiB: 100, Created: ago(120), FreeAllowance: true},
	}
	r.EKSClusters = []inventory.EKSInfo{
		{Name: "empty-eks", Status: "ACTIVE", Version: "1.33", Tier: "standard", VpcID: "vpc-eks-empty", Created: ago(30)},
		{Name: "busy-ng", Status: "ACTIVE", NodeGroups: 2, Created: ago(30)},
		{Name: "fargate-only", Status: "ACTIVE", Fargate: 1, Created: ago(30)},
		{Name: "selfmanaged", Status: "ACTIVE", Created: ago(30)},
		{Name: "auto-idle", Status: "ACTIVE", AutoMode: true, Created: ago(30)}, // EC2 hides Auto Mode instances
		{Name: "hybrid", Status: "ACTIVE", Hybrid: true, Created: ago(30)},
		{Name: "creating", Status: "CREATING", Created: ago(0)},
		{Name: "new-empty", Status: "ACTIVE", VpcID: "vpc-new", Created: ago(2)},
		{Name: "argo-hub", Status: "ACTIVE", Capabilities: 1, Created: ago(30)},          // managed Argo CD runs inside EKS
		{Name: "asg-zero", Status: "ACTIVE", Created: ago(30)},                           // self-managed group scaled to 0
		{Name: "untagged-nodes", Status: "ACTIVE", VpcID: "vpc-nodes", Created: ago(30)}, // an instance in its VPC
		{Name: "mng-tagged", Status: "ACTIVE", Created: ago(30)},
		{Name: "auto-tagged", Status: "ACTIVE", Created: ago(30)},
		{Name: "gone-node", Status: "ACTIVE", Tier: "tier-xl", VpcID: "vpc-gone", Created: ago(30)}, // its only node is terminated
		{Name: "protected", Status: "ACTIVE", VpcID: "vpc-protected", DeletionProtection: true, Created: ago(30)},
		{Name: "protected-new", Status: "ACTIVE", VpcID: "vpc-protected-new", DeletionProtection: true, Created: ago(2)},
		{Name: "no-vpc", Status: "ACTIVE", Created: ago(30)}, // DescribeCluster gave no VPC: unknown
	}
	r.EKSAutoScaling = map[string]int{"asg-zero": 1}
	nat := func(id string, age int) ec2types.NatGateway {
		return ec2types.NatGateway{NatGatewayId: aws.String(id), VpcId: aws.String("vpc-b"), State: ec2types.NatGatewayStateAvailable, CreateTime: ago(age)}
	}
	byENI, byIP, ipElsewhere, proxy, detached := nat("nat-eni", 40), nat("nat-ip", 40), nat("nat-ip-other-vpc", 40), nat("nat-proxy", 40), nat("nat-detached", 40)
	byENI.NatGatewayAddresses = []ec2types.NatGatewayAddress{{NetworkInterfaceId: aws.String("eni-nat"), PrivateIp: aws.String("10.0.1.5")}}
	byIP.NatGatewayAddresses = []ec2types.NatGatewayAddress{{NetworkInterfaceId: aws.String("eni-other"), PrivateIp: aws.String("10.0.9.9")}}
	// The same address as a next hop in another VPC says nothing about this NAT.
	ipElsewhere.NatGatewayAddresses = []ec2types.NatGatewayAddress{{NetworkInterfaceId: aws.String("eni-third"), PrivateIp: aws.String("10.0.7.7")}}
	// A Network Firewall proxy: apps reach it by hostname through its VPC endpoint, no route needed.
	proxy.AttachedAppliances = []ec2types.NatGatewayAttachedAppliance{{AttachmentState: ec2types.NatGatewayApplianceStateAttached, VpcEndpointId: aws.String("vpce-fw")}}
	detached.AttachedAppliances = []ec2types.NatGatewayAttachedAppliance{{AttachmentState: ec2types.NatGatewayApplianceStateDetached}}
	r.NatGateways = append(r.NatGateways, nat("nat-unrouted", 40), nat("nat-fresh", 0), byENI, byIP, ipElsewhere, proxy, detached)
	// Route targets: a NAT by ID, a NAT's network interface, a NAT's address as a VPC Route Server next hop.
	r.NATRouted = map[string]bool{"nat:nat-1": true, "nat:nat-2": true, "eni:eni-nat": true, "ip:vpc-b/10.0.9.9": true, "ip:vpc-z/10.0.7.7": true}
	tagged := func(id, key, value string, state ec2types.InstanceStateName) ec2types.Instance {
		in := inst(id, "m7g.large", state, "")
		in.Tags = []ec2types.Tag{{Key: aws.String(key), Value: aws.String(value)}}
		return in
	}
	vpcNode := inst("i-vpc-node", "m7g.large", ec2types.InstanceStateNameStopped, "")
	vpcNode.VpcId = aws.String("vpc-nodes")
	r.Instances = append(r.Instances, vpcNode,
		tagged("i-mng", "eks:cluster-name", "mng-tagged", ec2types.InstanceStateNameRunning),
		tagged("i-auto", "eks:eks-cluster-name", "auto-tagged", ec2types.InstanceStateNameRunning),
		tagged("i-gone", "kubernetes.io/cluster/gone-node", "owned", ec2types.InstanceStateNameTerminated))
	node := inst("i-eks-node", "m7g.large", ec2types.InstanceStateNameRunning, "")
	node.Tags = []ec2types.Tag{{Key: aws.String("kubernetes.io/cluster/selfmanaged"), Value: aws.String("owned")}}
	r.Instances = append(r.Instances, node)
	for i := range r.CacheClusters {
		r.CacheClusters[i].Created = ago(100)
		if r.CacheClusters[i].ID == "fresh" {
			r.CacheClusters[i].Created = ago(3) // skipped because it's new, with a known date
		}
	}
	r.ClassicLBs = []inventory.ClassicLB{
		{Name: "clb-empty", DNS: "clb-empty-1.eu-west-1.elb.amazonaws.com", Created: ago(90)},
		{Name: "clb-down", DNS: "clb-down-1.eu-west-1.elb.amazonaws.com", Created: ago(90), Instances: 2},
		{Name: "clb-new", Created: ago(0)},
		{Name: "clb-busy", Created: ago(90), Instances: 2, InService: 2},
		{Name: "clb-quiet", Created: ago(90), Instances: 1, InService: 1},
		{Name: "clb-unknown", Created: ago(90), Instances: 3, InService: -1}, // health unread
		{Name: "clb-asg", Created: ago(90)},                                  // an Auto Scaling group registers with it
		{Name: "clb-k8s", Created: ago(90), Kubernetes: "prod"},              // a Kubernetes Service made it
		{Name: "clb-tags-unread", Created: ago(90), TagsUnknown: true},
		{Name: "clb-silent", Created: ago(90), Instances: 1, InService: 1}, // no HealthyHostCount series
	}
	r.ASGLoadBalancers = map[string][]string{"clb:clb-asg": {"web-b", "web-a"}}
	// EC2 instances for idle-ec2 (current types, no VPC: other checks ignore them).
	running := func(id, typ, op string, launched int) ec2types.Instance {
		return ec2types.Instance{InstanceId: aws.String(id), InstanceType: ec2types.InstanceType(typ), UsageOperation: aws.String(op),
			State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, LaunchTime: ago(launched)}
	}
	win := running("i-idle-win", "m5.large", "RunInstances:0006", 90)
	win.PlatformDetails = aws.String("Windows with SQL Server Standard")
	spot, asgMember, karp, operated := running("i-spot", "m5.large", "RunInstances", 90), running("i-asg", "m5.large", "RunInstances", 90),
		running("i-karpenter", "m5.large", "RunInstances", 90), running("i-operated", "m5.large", "RunInstances", 90)
	spot.InstanceLifecycle = ec2types.InstanceLifecycleTypeSpot
	asgMember.Tags = []ec2types.Tag{{Key: aws.String("aws:autoscaling:groupName"), Value: aws.String("web")}}
	karp.Tags = []ec2types.Tag{{Key: aws.String("karpenter.sh/nodepool"), Value: aws.String("default")}}
	operated.Operator = &ec2types.OperatorResponse{Managed: aws.Bool(true), Principal: aws.String("eks.amazonaws.com")}
	cr := running("i-cr", "m7i.large", "RunInstances", 90)
	cr.CapacityReservationId = aws.String("cr-0dr")
	r.LoadBalancers = append(r.LoadBalancers, inventory.LoadBalancer{ARN: "arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/web-alb/1",
		Name: "web-alb", Type: "application", TargetGroups: 1, Targets: 2, TargetInstances: []string{"i-behind-lb"},
		TargetIPs: []inventory.TargetIP{{IP: "10.1.2.3", VPC: "vpc-app"}}})
	ipTarget, ipTwin := running("i-by-ip", "m7i.large", "RunInstances", 90), running("i-same-ip", "m7i.large", "RunInstances", 90)
	ipTarget.VpcId, ipTwin.VpcId = aws.String("vpc-app"), aws.String("vpc-other") // the same private IP in another VPC is another machine
	ipTarget.NetworkInterfaces = []ec2types.InstanceNetworkInterface{{PrivateIpAddresses: []ec2types.InstancePrivateIpAddress{
		{PrivateIpAddress: aws.String("10.1.0.9")}, {PrivateIpAddress: aws.String("10.1.2.3")}}}} // a secondary IP
	ipTarget.PrivateIpAddress, ipTwin.PrivateIpAddress = aws.String("10.1.0.9"), aws.String("10.1.2.3")
	big, bigQuiet := running("i-big", "m7i.16xlarge", "RunInstances", 90), running("i-big-quiet", "m7i.16xlarge", "RunInstances", 90)
	big.CpuOptions = &ec2types.CpuOptions{CoreCount: aws.Int32(32), ThreadsPerCore: aws.Int32(2)} // 64 vCPUs: the bound is 0.78%
	bigQuiet.CpuOptions = big.CpuOptions
	r.Instances = append(r.Instances, running("i-nightly", "m7i.large", "RunInstances", 90), running("i-behind-lb", "m7i.large", "RunInstances", 90), cr,
		ipTarget, ipTwin, big, bigQuiet)
	// Disks for idle-attached-ebs: an idle data disk (found), a busy one, the
	// root disk, and a disk of an EMR node (EMR's, left to idle-emr).
	data, emrNode := running("i-data", "m7i.large", "RunInstances", 90), running("i-emr-core", "m5.xlarge", "RunInstances", 90)
	data.RootDeviceName, emrNode.RootDeviceName = aws.String("/dev/xvda"), aws.String("/dev/xvda")
	emrNode.Tags = []ec2types.Tag{{Key: aws.String("aws:elasticmapreduce:job-flow-id"), Value: aws.String("j-1ABC")}}
	r.Instances = append(r.Instances, data, emrNode)
	attached := func(id, inst, dev string, size int32) ec2types.Volume {
		v := vol(id, ec2types.VolumeTypeGp3, size, ec2types.VolumeStateInUse)
		v.Attachments = []ec2types.VolumeAttachment{{InstanceId: aws.String(inst), Device: aws.String(dev), State: ec2types.VolumeAttachmentStateAttached}}
		return v
	}
	r.Volumes = append(r.Volumes, attached("vol-idle-data", "i-data", "/dev/sdf", 200), attached("vol-busy-data", "i-data", "/dev/sdg", 100),
		attached("vol-data-root", "i-data", "/dev/xvda", 8), attached("vol-emr-hdfs", "i-emr-core", "/dev/sdb", 500))
	r.Instances = append(r.Instances, running("i-idle", "t3.large", "RunInstances", 90), win,
		running("i-busy", "m7i.large", "RunInstances", 90), running("i-chatty", "m7i.large", "RunInstances", 90),
		spot, asgMember, karp, operated, running("i-young", "m7i.large", "RunInstances", 3), running("i-silent", "m7i.large", "RunInstances", 90))
	// Versioned buckets (all abort failed uploads, so s3-abort-mpu ignores them).
	bucket := func(name, versioning string) inventory.Bucket {
		return inventory.Bucket{Name: name, Region: "eu-west-1", Versioning: versioning, AbortsMPU: true, HasLifecycle: true}
	}
	kept, suspended, expire, mfa, locked, lockUnread := bucket("versions-kept", "Enabled"), bucket("versions-suspended", "Suspended"),
		bucket("versions-expire", "Enabled"), bucket("versions-mfa", "Enabled"), bucket("versions-locked", "Enabled"), bucket("versions-lock-unread", "Enabled")
	kept.HasLifecycle = false // no rules at all (AbortsMPU stays set only so s3-abort-mpu ignores it)
	expire.ExpiresNoncurrent = true
	mfa.MFADelete = true
	locked.ObjectLock, lockUnread.ObjectLock = "Enabled", "?"
	r.Buckets = append(r.Buckets, kept, suspended, expire, mfa, locked, lockUnread, bucket("versioning-unread", "?"), bucket("never-versioned", ""))
	r.HealthChecks = []inventory.HealthCheck{
		{ID: "hc-unused", Type: "HTTPS_STR_MATCH", Target: "api.example.com", Options: 2},
		{ID: "hc-basic", Type: "TCP", Target: "192.0.2.10"}, // no optional features: unpriced
		{ID: "hc-used", Type: "HTTPS", Options: 1, Used: true},
		{ID: "hc-managed", Type: "HTTP", Managed: "servicediscovery.amazonaws.com"},
		{ID: "hc-watched", Type: "HTTP", Watched: true},
	}
	r.CapacityListed = true
	r.CapacityReservations = []inventory.CapacityReservation{{ID: "cr-0123456789abcdef0", Type: "m5.large", Platform: "Linux/UNIX", AZ: "eu-west-1a",
		Total: 4, Available: 2, State: "active", Kind: "default", Start: *ago(60), Billed: true, Owned: true, Owner: "123456789012"}}
	r.AlarmsListed = true
	r.Alarms = append(r.Alarms, inventory.Alarm{Name: "cpu-high-i-0deadbeef00000000", ARN: "arn:aws:cloudwatch:eu-west-1:123456789012:alarm:cpu-high-i-0deadbeef00000000",
		Namespace: "AWS/EC2", Metric: "CPUUtilization", Dimensions: map[string]string{"InstanceId": "i-0deadbeef00000000"},
		State: "INSUFFICIENT_DATA", Updated: *ago(60), Transitioned: *ago(60)})
	if r.Exists == nil {
		r.Exists = map[string]map[string]bool{}
	}
	r.Exists["InstanceId"] = map[string]bool{}
	r.ZonesListed = true
	r.Zones = []inventory.HostedZone{
		{ID: "Z0EMPTY", Name: "old.example.com", Records: 2, DNSSEC: "none"},
		{ID: "Z0FULL", Name: "app.example.com", Records: 14},
		{ID: "Z0PRIVATE", Name: "corp.internal", Records: 2, Private: true},
		{ID: "Z0LINKED", Name: "svc.local", Records: 2, Linked: "servicediscovery.amazonaws.com"},
		{ID: "Z0SIGNED", Name: "signed.example.com", Records: 2, DNSSEC: "signed"},
	}
	db := func(id, class, engine, version string) inventory.DBInstance {
		return inventory.DBInstance{ID: id, ARN: "arn:aws:rds:eu-west-1:123456789012:db:" + id, ResourceID: "db-RES-" + id,
			Class: class, Engine: engine, Version: version, Status: "available", License: "general-public-license",
			StorageType: "gp3", GiB: 100, Created: ago(200)}
	}
	protected, primary, replica, member, young, stopped := db("protected-db", "db.m5.large", "mysql", "8.0.36"), db("primary", "db.m5.large", "mysql", "8.0.36"),
		db("replica", "db.m5.large", "mysql", "8.0.36"), db("member", "db.r4.large", "aurora-mysql", "8.0.mysql_aurora.3.05.2"),
		db("young-db", "db.m5.large", "mysql", "8.0.36"), db("stopped-db", "db.m4.large", "mysql", "8.0.36")
	protected.DeletionProtection = true
	primary.Replicas, replica.ReplicaOf, member.Cluster, young.Created, stopped.Status = 1, "primary", "aurora-1", ago(3), "stopped"
	r.DBInstances = []inventory.DBInstance{
		db("old-m4", "db.m4.large", "mysql", "8.0.36"),         // Graviton-ready: compared with db.m6g.large
		db("old-m4-57", "db.m4.large", "mysql", "5.7.44"),      // not on Graviton: compared with db.m5.large
		db("old-t2", "db.t2.micro", "postgres", "16.4"),        // compared with db.t4g.micro
		db("old-m3-medium", "db.m3.medium", "mysql", "8.0.36"), // no current class of that size
		db("old-oracle", "db.r4.large", "oracle-se2", "19.0.0.0.ru-2024-01.rur-2024-01.r1"),
		db("current", "db.m6g.large", "postgres", "16.4"),
		db("idle-db", "db.m5.large", "postgres", "16.4"),
		db("idle-old", "db.m4.large", "mysql", "8.0.36"), // idle and old: idle-rds reports it on paid plans
		db("old-m2", "db.m2.xlarge", "mysql", "8.0.36"),  // its twin db.r6g.xlarge costs more
		db("busy-db", "db.m5.large", "postgres", "16.4"),
		db("silent-db", "db.m5.large", "postgres", "16.4"), // no CPUUtilization: not reporting
		db("nopriced-db", "db.m5.large", "postgres", "16.4"),
		protected, primary, replica, member, young, stopped,
		{ID: "reports-aurora-1", Class: "db.serverless", Engine: "aurora-mysql", Version: "8.0.mysql_aurora.3.08.0", Cluster: "reports-aurora", Status: "available", Created: ago(200)},
	}
	// aurora-serverless-pause: a Serverless v2 cluster held at 0.5 ACUs, idle half of every day.
	r.DBClusters = []inventory.DBCluster{{ID: "reports-aurora", ARN: "arn:aws:rds:eu-west-1:123456789012:cluster:reports-aurora", Engine: "aurora-mysql",
		Version: "8.0.mysql_aurora.3.08.0", Status: "available", StorageType: "aurora", ServerlessV2: true, MinACU: 0.5, MaxACU: 8,
		Members: []inventory.DBClusterMember{{ID: "reports-aurora-1", Writer: true, Tier: 1}}}}
	if r.Usage != nil {
		r.Usage.Tail = tailUsage()
		in := func(days int) time.Time { return now.Add(time.Duration(days) * 24 * time.Hour) }
		r.Usage.Reservations = []inventory.Reservation{
			{Kind: "EC2", ID: "ri-ending", What: "m5.large", Count: 4, State: "active", End: in(10)},
			{Kind: "EC2", ID: "ri-later", What: "m5.large", Count: 4, State: "active", End: in(40)},
			{Kind: "RDS", ID: "rdsri-ended", ARN: "arn:aws:rds:eu-west-1:123456789012:ri:rdsri-ended", What: "db.r6g.large", Count: 2, State: "retired", End: in(-10)},
			{Kind: "ElastiCache", ID: "rcn-long-gone", ARN: "arn:aws:elasticache:eu-west-1:123456789012:reserved-instance:rcn-long-gone", What: "cache.r6g.large", Count: 1, State: "retired", End: in(-40)},
			{Kind: "OpenSearch", ID: "os-ending", What: "r6g.large.search", Count: 3, State: "active", End: in(20)},
			{Kind: "Savings Plan", ID: "sp-ending", ARN: "arn:aws:savingsplans::123456789012:savingsplan/sp-ending", What: "Compute", Commitment: "1.25", State: "active", End: in(25)},
		}
	}
	return r
}

// tailUsage is what the paid long-tail checks read on the fixture.
func tailUsage() inventory.Tail {
	data := func(n int, typ string) inventory.NodeGroup {
		return inventory.NodeGroup{Role: "data", Type: typ, Count: n}
	}
	return inventory.Tail{
		DDBPrice:     ddbTestPrices(),
		IndexReads:   map[string]float64{"events/by-user": 4e6, "events/by-day": 0},
		IndexWrites:  map[string]float64{"events/by-user": 9e6, "events/by-day": 9e6},
		IndexSince:   map[string]bool{"events/by-user": true, "events/by-day": true},
		TableReports: map[string]bool{"events": true},
		DDBReads:     map[string]float64{"archive": 1e6},
		DDBWrites:    map[string]float64{"archive": 1e5},
		LambdaErrors: map[string]float64{"worker": 2e5}, LambdaInvocations: map[string]float64{"worker": 1e6},
		// worker:live keeps 10 warm and never needs more than 2 at once.
		PCConfigs: []inventory.PCConfig{{Function: "worker", Qualifier: "live", ARN: "arn:aws:lambda:eu-west-1:123456789012:function:worker:live",
			Allocated: 10, Requested: 10, Status: "READY", Modified: *ago(60)}},
		PCPeak:          map[string]float64{"arn:aws:lambda:eu-west-1:123456789012:function:worker:live": 2},
		PCSpill:         map[string]float64{"arn:aws:lambda:eu-west-1:123456789012:function:worker:live": 0},
		PCFnInvocations: map[string]float64{"worker": 5000},
		AuroraHours:     map[string]*inventory.Hourly{"reports-aurora-1": auroraUse(dayBusy, 0.5)},
		AuroraBlocked:   map[string]string{},
		AuroraZeroMin:   map[string]bool{"aurora-mysql|8.0.mysql_aurora.3.08.0": true},
		ACUHour:         map[string]float64{"aurora-mysql|standard": 0.14},
		// An empty NextGen collection group held at 2 and 2 OCUs.
		AOSS: &inventory.AOSS{GroupsRead: true, IndexingOCUHour: 0.24, SearchOCUHour: 0.24, Groups: []inventory.AOSSGroup{{ID: "ng0", Name: "ng-empty", Generation: "NEXTGEN",
			Created: *ago(400), MinIndexing: 2, MaxIndexing: 96, MinSearch: 2, MaxSearch: 96, NowIndexing: -1, NowSearch: -1}}},
		LambdaDurationSum: map[string]float64{"worker": 1.5e9}, LambdaDurationMax: map[string]float64{"worker": 30000},
		LambdaPCInvocations: map[string]float64{"worker": 0}, LambdaAsync: map[string]float64{"worker": 0}, LambdaRecentErrors: map[string]float64{"worker": 3e4},
		ECSServices: []inventory.ECSService{{ARN: "arn:aws:ecs:eu-west-1:123456789012:service/prod/web", Name: "web", Cluster: "prod", Status: "ACTIVE", LaunchType: "FARGATE",
			Desired: 4, Running: 4, TaskDef: "arn:aws:ecs:eu-west-1:123456789012:task-definition/web:3", Deployments: 1, PrimarySince: ago(60), Controller: "ECS", Created: ago(61)}},
		ECSTaskSizes: map[string]inventory.ECSTaskSize{"arn:aws:ecs:eu-west-1:123456789012:task-definition/web:3": {CPU: 2048, MemMiB: 8192, Fargate: true}},
		ECSCPUMax:    map[string]float64{"arn:aws:ecs:eu-west-1:123456789012:service/prod/web": 20},
		ECSMemMax:    map[string]float64{"arn:aws:ecs:eu-west-1:123456789012:service/prod/web": 30},
		FargatePrice: map[string]float64{"Fargate-vCPU-Hours:perCPU": 0.04048, "Fargate-GB-Hours": 0.004445, "Fargate-ARM-vCPU-Hours:perCPU": 0.03238, "Fargate-ARM-GB-Hours": 0.00356},
		GAFlows:      map[string]float64{"a0000000-0000-4000-8000-000000000002": 0},
		GABytes:      map[string]float64{"a0000000-0000-4000-8000-000000000002": 0},
		Domains: []inventory.Domain{
			{Name: "logs-old", Engine: "Elasticsearch", Version: "7.4", VolumeType: "gp3", VolumeGiB: 100,
				Nodes: []inventory.NodeGroup{data(3, "r6g.large.search"), {Role: "master", Type: "m6g.large.search", Count: 3}}},
			{Name: "search-56", Engine: "Elasticsearch", Version: "5.6", Nodes: []inventory.NodeGroup{data(2, "r6g.xlarge.search")}},
			{Name: "os-213", Engine: "OpenSearch", Version: "2.13", Nodes: []inventory.NodeGroup{data(2, "r6g.large.search")}},
			{Name: "idle-search", Engine: "OpenSearch", Version: "2.19", VolumeType: "gp3", VolumeGiB: 50, Nodes: []inventory.NodeGroup{data(2, "r6g.large.search")}},
			{Name: "busy-search", Engine: "OpenSearch", Version: "2.19", Nodes: []inventory.NodeGroup{data(2, "r6g.large.search")}},
			{Name: "quiet-unknown", Engine: "OpenSearch", Version: "2.19", Nodes: []inventory.NodeGroup{data(1, "r6g.large.search")}},
		},
		Endpoints: []inventory.SageMakerEndpoint{
			{Name: "churn-model", Created: ago(60), Variants: []inventory.Variant{{Name: "AllTraffic", Type: "ml.g5.xlarge", Count: 2}}},
			{Name: "live-model", Created: ago(60), Variants: []inventory.Variant{{Name: "AllTraffic", Type: "ml.m5.large", Count: 1}}},
			{Name: "new-model", Created: ago(2), Variants: []inventory.Variant{{Name: "AllTraffic", Type: "ml.m5.large", Count: 1}}},
			{Name: "silent-model", Created: ago(60), Variants: []inventory.Variant{{Name: "AllTraffic", Type: "ml.m5.large", Count: 1}}},
		},
		Warehouses: []inventory.Warehouse{
			{ID: "bi-cluster", NodeType: "ra3.xlplus", Status: "available", Nodes: 2, Created: ago(100), Pausable: true},
			{ID: "busy-wh", NodeType: "ra3.xlplus", Status: "available", Nodes: 2, Created: ago(100)},
			{ID: "paused-wh", NodeType: "ra3.xlplus", Status: "paused", Nodes: 2, Created: ago(100)},
			{ID: "old-ds2", NodeType: "ds2.xlarge", Status: "available", Nodes: 1, Created: ago(900)},
		},
		ClientVPNs: []inventory.ClientVPN{
			{ID: "cvpn-idle", Associations: []string{"cvpn-assoc-1", "cvpn-assoc-2"}, Created: ago(60)},
			{ID: "cvpn-busy", Associations: []string{"cvpn-assoc-3"}, Created: ago(60)},
			{ID: "cvpn-young", Associations: []string{"cvpn-assoc-4"}, Created: ago(3)},
			{ID: "cvpn-silent", Associations: []string{"cvpn-assoc-5"}, Created: ago(60)},
		},
		ClientVPNUse: map[string]float64{"cvpn-idle": 0, "cvpn-busy": 300, "cvpn-young": 0, "cvpn-silent": 0},
		VPNs:         []string{"vpn-down", "vpn-half", "vpn-silent", "vpn-nodata"},
		DBConnections: map[string]float64{"idle-db": 0, "idle-old": 0, "protected-db": 0, "primary": 0, "young-db": 0, "silent-db": 0,
			"busy-db": 12, "nopriced-db": 0},
		RDSHour:        map[string]float64{"idle-db": 0.178, "idle-old": 0.175, "protected-db": 0.171, "busy-db": 0.171},
		OffHours:       map[string]*inventory.Hourly{"rds:busy-db": officeHours(4, nil)},
		ClassicTraffic: map[string]float64{"clb-busy": 1e6, "clb-quiet": 0, "clb-silent": 0},
		// Highest daily average CPU and highest daily bytes in and out.
		MemDBClusters: []inventory.MemDBCluster{{Name: "sessions-mdb", ARN: "arn:aws:memorydb:eu-west-1:123456789012:cluster/sessions-mdb",
			NodeType: "db.r6g.large", Engine: "valkey", Status: "available", Nodes: []string{"0001-001", "0001-002"}, Created: ago(60)}},
		MemDBNodeDims: map[string][][]string{"sessions-mdb": {{"ClusterName", "sessions-mdb", "NodeName", "0001-001"}, {"ClusterName", "sessions-mdb", "NodeName", "0001-002"}}},
		MemDBCommands: map[string]float64{"sessions-mdb": 0},
		MemDBConns:    map[string]float64{"sessions-mdb": 3},
		MemDBHour:     map[string]float64{"sessions-mdb": 0.2163},
		EMRClusters:   []inventory.EMRCluster{{ID: "j-1ABC", Name: "nightly-etl", State: "WAITING", Created: ago(60), Apps: []string{"Spark", "Hive"}}},
		EMRIdle:       map[string]float64{"j-1ABC": 14 * 288},
		EMRSamples:    map[string]float64{"j-1ABC": 14 * 288},
		EMRHDFS:       map[string]float64{"j-1ABC": 2},
		EMRAutoIdle:   map[string]float64{"j-1ABC": 0}, EMRAutoSamples: map[string]float64{"j-1ABC": 0},
		EMRFee: map[string]float64{"m5.xlarge": 0.048},
		EBSOps: map[string]float64{"vol-idle-data": 0, "vol-busy-data": 4200, "vol-data-root": 0, "vol-emr-hdfs": 0},
		EC2CPU: map[string]float64{"i-idle": 0.7, "i-idle-win": 1.2, "i-busy": 35, "i-chatty": 0.5, "i-spot": 0.1, "i-asg": 0.1, "i-karpenter": 0.1, "i-operated": 0.1, "i-young": 0.1, "i-silent": 0.1,
			"i-nightly": 1.4, "i-behind-lb": 0.4, "i-cr": 0.3, "i-by-ip": 0.3, "i-same-ip": 0.3, "i-big": 0.2, "i-big-quiet": 0.1},
		// The highest single sample: i-nightly runs 20 minutes at 100% each night.
		EC2CPUPeak: map[string]float64{"i-idle": 4, "i-idle-win": 6, "i-busy": 90, "i-chatty": 3, "i-spot": 1, "i-asg": 1, "i-karpenter": 1, "i-operated": 1, "i-young": 1, "i-silent": 1,
			"i-nightly": 100, "i-behind-lb": 3, "i-cr": 2, "i-by-ip": 2, "i-same-ip": 2,
			"i-big": 3, "i-big-quiet": 0.5}, // i-big: one thread for a few minutes is 1.6% of 64 vCPUs
		EC2NetIn: map[string]float64{"i-idle": 1e6, "i-idle-win": 2e6, "i-busy": 1e6, "i-chatty": 40e6, "i-spot": 1e5, "i-asg": 1e5, "i-karpenter": 1e5, "i-operated": 1e5, "i-young": 1e5, "i-silent": 1e5,
			"i-nightly": 1e6, "i-behind-lb": 1e6, "i-cr": 1e6, "i-by-ip": 1e6, "i-same-ip": 1e6, "i-big": 1e6, "i-big-quiet": 1e6},
		EC2NetOut: map[string]float64{"i-idle": 2e6, "i-idle-win": 1e6, "i-busy": 1e6, "i-chatty": 1e6, "i-spot": 1e5, "i-asg": 1e5, "i-karpenter": 1e5, "i-operated": 1e5, "i-young": 1e5, "i-silent": 1e5,
			"i-nightly": 1e6, "i-behind-lb": 1e6, "i-cr": 1e6, "i-by-ip": 1e6, "i-same-ip": 1e6, "i-big": 1e6, "i-big-quiet": 1e6},
		EC2Hour: map[string]float64{"i-idle": 0.0832, "i-behind-lb": 0.1008, "i-emr-core": 0.192, "i-eks-node": 0.0816},
		// selfmanaged's pods request 80% of its node's CPU and use a quarter of that.
		EKSFit: map[string]*inventory.EKSFit{"selfmanaged": func() *inventory.EKSFit {
			f := &inventory.EKSFit{NodesNow: 1}
			for range 14 * 86400 / 300 {
				f.CPUReserved, f.MemReserved = append(f.CPUReserved, 80), append(f.MemReserved, 60)
				f.CPUUsed, f.MemUsed = append(f.CPUUsed, 20), append(f.MemUsed, 25)
			}
			return f
		}()},
		Firewalls: []inventory.Firewall{
			{Name: "fw-idle", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-idle", VPC: "vpc-fw", Endpoints: []string{"vpce-fw-a", "vpce-fw-b"}, Oldest: ago(60), Listed: true},
			{Name: "fw-busy", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-busy", VPC: "vpc-fw", Endpoints: []string{"vpce-busy"}, Oldest: ago(60), Listed: true, Active: true},
			{Name: "fw-new", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-new", VPC: "vpc-fw", Endpoints: []string{"vpce-new"}, Oldest: ago(5), Listed: true},
			{Name: "fw-age-unknown", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-age-unknown", VPC: "vpc-fw", Endpoints: []string{"vpce-x"}, Listed: true},
			{Name: "fw-unlisted", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-unlisted", VPC: "vpc-fw", Endpoints: []string{"vpce-y"}, Oldest: ago(60)}, // ListMetrics failed
			{Name: "fw-tgw", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-tgw", Endpoints: []string{"vpce-z"}, Oldest: ago(60), Listed: true, Unjudged: "it is attached to a transit gateway"},
			{Name: "fw-protected", ARN: "arn:aws:network-firewall:eu-west-1:123456789012:firewall/fw-protected", VPC: "vpc-other", Endpoints: []string{"vpce-p"}, Oldest: ago(60), Listed: true, DeleteProtection: true},
		},
		// Sums of TunnelState: 0 = every tunnel down in every sample.
		VPNTunnelState: map[string]float64{"vpn-down": 0, "vpn-half": 1440, "vpn-silent": 0},
		Kafka: []inventory.KafkaCluster{
			{Name: "events", ARN: "arn:aws:kafka:eu-west-1:123456789012:cluster/events/0d2f6a1c-1", InstanceType: "kafka.m5.large", BrokerIDs: []string{"1", "2", "3"}, StorageGiB: 100, Created: ago(60)},
			{Name: "busy-kafka", ARN: "arn:aws:kafka:eu-west-1:123456789012:cluster/busy/0d2f6a1c-1", InstanceType: "kafka.m5.large", BrokerIDs: []string{"1", "2"}, StorageGiB: 100, Created: ago(60)},
			{Name: "partial", ARN: "arn:aws:kafka:eu-west-1:123456789012:cluster/partial/0d2f6a1c-1", InstanceType: "kafka.m5.large", BrokerIDs: []string{"1", "2"}, StorageGiB: 100, Created: ago(60)},
			{Name: "express-k", ARN: "arn:aws:kafka:eu-west-1:123456789012:cluster/express/0d2f6a1c-1", InstanceType: "express.m7g.large", BrokerIDs: []string{"1", "2", "3"}, Created: ago(60)},
		},
		Attachments: []inventory.Attachment{
			{ID: "tgw-attach-idle", Gateway: "tgw-1", Type: "vpc", Resource: "vpc-1", State: "available", Created: ago(60)},
			{ID: "tgw-attach-busy", Gateway: "tgw-1", Type: "vpc", Resource: "vpc-2", State: "available", Created: ago(60)},
		},
		DomainRequests:  map[string]float64{"logs-old": 1e6, "search-56": 10, "os-213": 5, "idle-search": 0, "busy-search": 5e6, "quiet-unknown": 0},
		Invocations:     map[string]float64{"churn-model/AllTraffic": 0, "live-model/AllTraffic": 1e5, "new-model/AllTraffic": 0, "silent-model/AllTraffic": 0},
		Connections:     map[string]float64{"bi-cluster": 0, "busy-wh": 500, "paused-wh": 0, "old-ds2": 0},
		AttachmentBytes: map[string]float64{"tgw-attach-idle": 0, "tgw-attach-busy": 1e9},
		CacheCommands:   map[string]float64{"sessions": 0, "old-memcache": 0, "v6-cache": 5e6, "fresh": 0},
		CacheItems:      map[string]float64{"sessions": 0, "old-memcache": 0, "v6-cache": 900, "fresh": 0},
		KafkaTraffic:    map[string]float64{"events": 0, "busy-kafka": 1e9, "partial": 0, "express-k": 0},
		Reporting: map[string]bool{"domain:logs-old": true, "domain:search-56": true, "domain:os-213": true, "domain:idle-search": true, "domain:busy-search": true,
			"variant:churn-model/AllTraffic": true, "variant:live-model/AllTraffic": true, "variant:new-model/AllTraffic": true,
			"warehouse:bi-cluster": true, "warehouse:busy-wh": true, "warehouse:old-ds2": true,
			"attachment:tgw-attach-idle": true, "attachment:tgw-attach-busy": true,
			"cache:sessions/sessions-0001-001/0001": true, "cache:sessions/sessions-0001-002/0001": true,
			"cache:old-memcache/old-memcache/0001": true, "cache:old-memcache/old-memcache/0002": true, "cache:v6-cache/v6-cache/0001": true,
			"kafka:events/1": true, "kafka:events/2": true, "kafka:events/3": true, "kafka:busy-kafka/1": true, "kafka:busy-kafka/2": true,
			"kafka:partial/1": true, "kafka:express-k/1": true, "kafka:express-k/2": true, "kafka:express-k/3": true,
			"cvpn:cvpn-idle": true, "cvpn:cvpn-busy": true, "cvpn:cvpn-young": true, // cvpn-silent: not reporting
			"vpn:vpn-down": true, "vpn:vpn-half": true, "vpn:vpn-nodata": true, // vpn-silent: not reporting
			"rds:idle-db": true, "rds:idle-old": true, "rds:protected-db": true, "rds:primary": true, "rds:young-db": true,
			"rds:busy-db": true, "rds:nopriced-db": true, // silent-db: not reporting
			"clb:clb-busy": true, "clb:clb-quiet": true, // clb-silent: not reporting
			"ec2:i-idle": true, "ec2:i-idle-win": true, "ec2:i-busy": true, "ec2:i-chatty": true, "ec2:i-spot": true, "ec2:i-asg": true,
			"ec2:i-karpenter": true, "ec2:i-operated": true, "ec2:i-young": true, // i-silent: not reporting
			"ec2:i-nightly": true, "ec2:i-behind-lb": true, "ec2:i-cr": true, "ec2:i-by-ip": true, "ec2:i-same-ip": true, "ec2:i-big": true, "ec2:i-big-quiet": true,
			"mdb:sessions-mdb/0": true, "mdb:sessions-mdb/1": true, "emr-hdfs:j-1ABC": true, "ecs:arn:aws:ecs:eu-west-1:123456789012:service/prod/web": true, "ecs-mem:arn:aws:ecs:eu-west-1:123456789012:service/prod/web": true, "ga-h:a0000000-0000-4000-8000-000000000002": true,
			"ebs:vol-idle-data": true, "ebs:vol-busy-data": true, "ebs:vol-data-root": true, "ebs:vol-emr-hdfs": true},
		SearchHour:      map[string]float64{"r6g.large.search": 0.167, "m6g.large.search": 0.128, "r6g.xlarge.search": 0.335},
		SearchExtraHour: map[string]float64{"r6g.large.search": 0.167}, // m6g falls back to its instance price
		SearchGBMonth:   map[string]float64{"gp3": 0.122},
		SearchNIHHour:   0.0065,
		SageMakerHour:   map[string]float64{"ml.g5.xlarge": 1.408, "ml.m5.large": 0.115},
		RedshiftHour:    map[string]float64{"ra3.xlplus": 1.086},
		AttachmentHour:  0.05,
		CacheNodeHour:   map[string]float64{"redis/cache.m5.large": 0.156, "memcached/cache.t2.medium": 0.068},
		KafkaBrokerHour: map[string]float64{"kafka.m5.large": 0.21, "express.m7g.large": 0.408},
		KafkaGBMonth:    0.10,
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestUnusedAMIs(t *testing.T) {
	got := UnusedAMIs(fixture(), opts())
	if len(got) != 1 || got[0].Resource != "ami-old-unused (web-2025)" {
		t.Fatalf("got %+v, want only ami-old-unused", got)
	}
	f := got[0]
	// Both snapshots come from one disk: the lineage is priced once, at its
	// largest snapshot (20 GiB, archive tier, $0.0125).
	if !f.Priced || !near(f.MonthlyUSD, 20*0.0125) || !strings.Contains(f.Detail, "2 snapshot(s) hold 20 GB") || f.Confidence != "" {
		t.Errorf("price/detail/confidence wrong: %v %q %q", f.MonthlyUSD, f.Detail, f.Confidence)
	}
	if !strings.Contains(strings.Join(f.Fix, "\n"), "deregister-image --region eu-west-1 --image-id ami-old-unused --delete-associated-snapshots") {
		t.Errorf("fix: %v", f.Fix)
	}

	// References unknown (a missing permission, an SCP, throttling) or
	// launch configurations in use: costhound doesn't guess. The finding
	// stays at low confidence, out of the total, with every delete a comment.
	unsure := func(name string, got []Finding, want int) {
		if len(got) != want {
			t.Fatalf("%s: got %d findings, want %d: %+v", name, len(got), want, got)
		}
		for _, f := range got {
			if f.Confidence != "low" || f.Priced || f.MonthlyUSD != 0 {
				t.Errorf("%s: %s must be low and unpriced: %+v", name, f.Resource, f)
			}
			for _, line := range f.Fix {
				if strings.Contains(line, "deregister-image") && !strings.HasPrefix(line, "# ") {
					t.Errorf("%s: %s: a runnable deregister: %q", name, f.Resource, line)
				}
			}
		}
	}
	r := fixture()
	r.ImageRefs = nil
	unsure("references unknown", UnusedAMIs(r, opts()), 2) // the template's AMI shows up too
	for _, n := range []int{1, -1} {
		r = fixture()
		r.LaunchConfigASGs = n
		unsure(fmt.Sprintf("launch configurations %d", n), UnusedAMIs(r, opts()), 1)
	}
	r = fixture()
	r.Skipped = append(r.Skipped, inventory.Skip{What: "ec2:DescribeInstances", Error: "UnauthorizedOperation"})
	if got := UnusedAMIs(r, opts()); got != nil {
		t.Errorf("without the instance list nothing may be called unused: %+v", got)
	}
}

func TestUnusedAMIGuards(t *testing.T) {
	dlm := amiAt("ami-dlm", "dlm-made", 300, ebsMap("snap-dlm", 8))
	dlm.Tags = []ec2types.Tag{{Key: aws.String("aws:dlm:lifecycle-policy-id"), Value: aws.String("policy-1")}}
	pending := amiAt("ami-pending", "pending", 300, ebsMap("snap-pend", 8))
	pending.State = ec2types.ImageStatePending
	recent := amiAt("ami-recent", "recent", 300, ebsMap("snap-rec", 8))
	recent.LastLaunchedTime = aws.String(now.AddDate(0, 0, -10).Format(time.RFC3339))
	stale := amiAt("ami-stale", "stale", 300, ebsMap("snap-stale", 8))
	stale.LastLaunchedTime = aws.String(now.AddDate(0, 0, -200).Format(time.RFC3339))
	stopped := inst("i-stopped", "m5.large", ec2types.InstanceStateNameStopped, "")
	stopped.ImageId = aws.String("ami-stopped")
	gone := inst("i-gone", "m5.large", ec2types.InstanceStateNameTerminated, "")
	gone.ImageId = aws.String("ami-terminated")
	r := &inventory.Region{Name: "eu-west-1", ImageRefs: map[string]bool{},
		Images: []ec2types.Image{dlm, pending, recent, stale,
			amiAt("ami-stopped", "stopped-use", 300, ebsMap("snap-stop", 8)),
			amiAt("ami-terminated", "only-terminated", 300, ebsMap("snap-term", 10))},
		Instances: []ec2types.Instance{stopped, gone}}
	got := byResource(UnusedAMIs(r, opts()))
	if len(got) != 2 {
		t.Fatalf("want only ami-stale and ami-terminated (DLM, pending, recently launched and stopped-instance AMIs stay quiet): %+v", got)
	}
	if f := got["ami-terminated (only-terminated)"]; f.Priced || !strings.Contains(f.Detail, "10 GB") {
		t.Errorf("a snapshot that isn't listed can't be priced: %+v", f)
	}
}

// 30 nightly AMIs of one disk free one disk's worth, not 30.
func TestUnusedAMIsShareADisk(t *testing.T) {
	r := &inventory.Region{Name: "eu-west-1", ImageRefs: map[string]bool{}}
	for i, size := range []int64{30, 31, 32} {
		id := fmt.Sprintf("n%d", i)
		r.Images = append(r.Images, amiAt("ami-"+id, "nightly-"+id, 100+i, ebsMap("snap-"+id, 40)))
		r.Snapshots = append(r.Snapshots, snap("snap-"+id, "vol-nightly", 100+i, 40, size, ec2types.SnapshotStateCompleted))
	}
	got := UnusedAMIs(r, opts())
	if len(got) != 1 || got[0].Resource != "3 AMIs of vol-nightly" || got[0].Key != "amis-of-vol-nightly" || !got[0].Priced || !near(got[0].MonthlyUSD, 32*0.05) ||
		strings.Count(strings.Join(got[0].Fix, "\n"), "deregister-image") != 3 || got[0].Facts["images"] != "ami-n0 ami-n1 ami-n2" {
		t.Fatalf("want one group priced at the disk's largest snapshot: %+v", got)
	}
	// Another snapshot of the same disk stays: deleting these frees less.
	r.Snapshots = append(r.Snapshots, snap("snap-kept", "vol-nightly", 5, 40, 33, ec2types.SnapshotStateCompleted))
	if got := UnusedAMIs(r, opts()); len(got) != 1 || got[0].Priced || !strings.Contains(got[0].Detail, "up to") {
		t.Errorf("a kept snapshot of the disk: unpriced, up to: %+v", got)
	}
	// An AMI something still uses shares one of the snapshots.
	r.Snapshots = r.Snapshots[:3]
	r.Images = append(r.Images, amiAt("ami-live", "live", 50, ebsMap("snap-n2", 40)))
	r.ImageRefs["ami-live"] = true
	if got := UnusedAMIs(r, opts()); len(got) != 1 || got[0].Priced {
		t.Errorf("a snapshot shared with a used AMI stays: unpriced: %+v", got)
	}
}

func TestIdleDedicatedHosts(t *testing.T) {
	got := IdleDedicatedHosts(fixture(), opts())
	if len(got) != 2 {
		t.Fatalf("got %d findings, want h-idle and h-reserved: %+v", len(got), got)
	}
	idle, reserved := got[0], got[1]
	if idle.Resource != "h-idle" || !idle.Priced || !near(idle.MonthlyUSD, 5.069*730) || !strings.Contains(idle.Detail, "$5.069/h, the us-east-1 On-Demand list price") || idle.Confidence != "" {
		t.Errorf("idle host wrong: %+v", idle)
	}
	if reserved.Resource != "h-reserved" || reserved.Priced || !strings.Contains(reserved.Detail, "hr-0abc") {
		t.Errorf("a reserved host saves nothing on release, so it must not be priced: %+v", reserved)
	}
	// DescribeHosts lists running instances only: a stopped BYOL instance
	// with host affinity still sits on its host.
	r := fixture()
	byol := inst("i-byol", "m5.large", ec2types.InstanceStateNameStopped, "")
	byol.Placement = &ec2types.Placement{HostId: aws.String("h-idle")}
	r.Instances = append(r.Instances, byol)
	if got := byResource(IdleDedicatedHosts(r, opts())); got["h-idle"].Check != "" {
		t.Errorf("a host with a stopped instance placed on it isn't empty: %+v", got)
	}
	r = fixture()
	r.Skipped = append(r.Skipped, inventory.Skip{What: "ec2:DescribeInstances", Error: "Throttling"})
	if got := IdleDedicatedHosts(r, opts()); got != nil {
		t.Errorf("without the instance list a stopped instance on a host can't be seen: %+v", got)
	}
	r = &inventory.Region{Name: "eu-west-1", Hosts: []inventory.Host{
		{ID: "h-mac", Family: "mac2", State: "available", Allocated: ago(0)},
		{ID: "h-odd", Family: "zz9", State: "available", Allocated: ago(30)},
	}}
	got = IdleDedicatedHosts(r, opts())
	if len(got) != 2 || got[0].Confidence != "low" || !strings.Contains(got[0].Fix[0], "24 hours") || got[1].Priced {
		t.Errorf("new host must be low confidence with the Mac note; unknown family unpriced: %+v", got)
	}
}

func TestDisabledKMSKeys(t *testing.T) {
	got := DisabledKMSKeys(fixture(), opts())
	if len(got) != 3 {
		t.Fatalf("got %+v, want the two disabled keys and the one without key material", got)
	}
	want := map[string]float64{"key-disabled (alias/old-app)": 1, "key-rotated": 3, "key-import": 1} // rotations cap at 2 extra versions
	for _, f := range got {
		fix := strings.Join(f.Fix, "\n")
		if w, ok := want[f.Resource]; !ok || !f.Priced || !near(f.MonthlyUSD, w) {
			t.Errorf("%s: $%.2f, want $%.2f", f.Resource, f.MonthlyUSD, w)
		}
		if !strings.Contains(fix, "schedule-key-deletion --region eu-west-1 --key-id "+f.Key) || !strings.Contains(fix, "# changed your mind during the 30-day wait: aws kms cancel-key-deletion --region eu-west-1 --key-id "+f.Key) {
			t.Errorf("%s: fix %v", f.Resource, f.Fix)
		}
		if withDefaults(f).Confidence != "low" {
			t.Errorf("%s: AWS calls disabling the safe alternative to deleting: low confidence", f.Resource)
		}
	}
}

func TestRDSOrphanSnapshots(t *testing.T) {
	got := RDSOrphanSnapshots(fixture(), opts())
	by := map[string]Finding{}
	for _, f := range got {
		by[f.Key] = f
	}
	if len(got) != 6 {
		t.Fatalf("got %d findings, want olddb, oldcluster, mixed, gone2, the DR copies, maz-cluster: %+v", len(got), got)
	}
	if f := by["db:olddb"]; !f.Priced || !near(f.MonthlyUSD, 200*0.095) || len(f.Fix) != 3 ||
		!strings.HasPrefix(f.Fix[1], "aws rds delete-db-snapshot --db-snapshot-identifier olddb-2025") || !strings.HasPrefix(f.Fix[2], "# aws rds delete-db-snapshot --db-snapshot-identifier olddb-final") ||
		!strings.Contains(f.Detail, "us-east-1 rate") {
		t.Errorf("olddb: priced at its largest snapshot, oldest first, the newest commented out: %+v", f)
	}
	if f := by["cluster:oldcluster"]; !f.Priced || !near(f.MonthlyUSD, 500*0.021) || !strings.Contains(f.Fix[1], "delete-db-cluster-snapshot") {
		t.Errorf("oldcluster (Aurora rate): %+v", f)
	}
	if f := by["cluster:maz-cluster"]; !f.Priced || !near(f.MonthlyUSD, 100*0.095) {
		t.Errorf("a Multi-AZ DB cluster's snapshots pay the RDS rate, and deleted clusters have no free allowance: %+v", f)
	}
	if f := by["db:mixed"]; f.Priced || !strings.Contains(f.Detail, "1 newer snapshot") {
		t.Errorf("mixed: a newer snapshot is kept, so unpriced: %+v", f)
	}
	if f := by["db:gone2"]; f.Priced || f.Confidence != "low" || !strings.Contains(f.Detail, "free backup allowance") {
		t.Errorf("gone2: the free allowance may cover it: %+v", f)
	}
	f := by["copy-db:prod-db"]
	if f.Priced || f.Confidence != "low" || !strings.Contains(f.Detail, "from us-east-1") || f.Facts["copy"] != "true" {
		t.Errorf("DR copies: low and unpriced: %+v", f)
	}
	for _, line := range f.Fix {
		if !strings.HasPrefix(line, "#") {
			t.Errorf("every delete of a DR copy must be a comment: %q", line)
		}
	}
	if _, ok := by["db:livedb"]; ok {
		t.Error("snapshots of a live database reported")
	}
}

func TestEmptyEKSClusters(t *testing.T) {
	got := byResource(EmptyEKSClusters(fixture(), opts()))
	if len(got) != 5 {
		t.Fatalf("want empty-eks, new-empty and gone-node; node groups, Fargate, Auto Mode, capabilities, a zero-size ASG, "+
			"tagged nodes (all three tags), a VPC with an instance, hybrid and creating stay quiet: %+v", got)
	}
	if f := got["empty-eks"]; !f.Priced || !near(f.MonthlyUSD, 73) || f.Confidence != "" || !strings.HasPrefix(f.Fix[1], "aws eks delete-cluster --region eu-west-1 --name empty-eks") {
		t.Errorf("empty-eks: %+v", f)
	}
	if f := got["new-empty"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[len(f.Fix)-1], "# aws eks delete-cluster") {
		t.Errorf("a cluster a few days old may still be set up: low, with the delete as a comment: %+v", f)
	}
	// A terminated node doesn't keep a cluster busy; an XL control plane is
	// the standard price plus the tier's rate.
	if f := got["gone-node"]; !near(f.MonthlyUSD, (0.10+1.65)*730) || f.Confidence != "" {
		t.Errorf("gone-node: %+v", f)
	}
	// Deletion protection was turned on on purpose: low, and nothing live
	// (neither the protection change nor the delete), old or new.
	for _, name := range []string{"protected", "protected-new"} {
		f := got[name]
		if f.Confidence != "low" || !strings.Contains(f.Why, "someone protected it on purpose") {
			t.Errorf("%s: %+v", name, f)
		}
		for _, line := range f.Fix {
			if !strings.HasPrefix(line, "#") {
				t.Errorf("%s: a live line on a protected cluster: %q", name, line)
			}
		}
	}
	if _, ok := got["no-vpc"]; ok {
		t.Error("a cluster with no VPC ID can't be checked for untagged nodes: unknown")
	}
	for _, skip := range []string{"ec2:DescribeInstances", "autoscaling:DescribeAutoScalingGroups"} {
		r := fixture()
		r.Skipped = append(r.Skipped, inventory.Skip{What: skip, Error: "Throttling"})
		if got := EmptyEKSClusters(r, opts()); got != nil {
			t.Errorf("without %s nodes may be invisible: %+v", skip, got)
		}
	}
}

func TestNATNoRoutes(t *testing.T) {
	got := byResource(NATNoRoutes(fixture(), opts()))
	if len(got) != 4 || got["nat-unrouted"].Check == "" || got["nat-fresh"].Check == "" || got["nat-detached"].Check == "" || got["nat-ip-other-vpc"].Check == "" {
		t.Fatalf("want nat-unrouted, nat-fresh, nat-detached and nat-ip-other-vpc (its address is a next hop in another VPC); quiet: routed by ID, by network interface, by a Route Server next hop in its VPC, with a proxy attached, deleted: %+v", got)
	}
	if f := got["nat-unrouted"]; !f.Priced || !near(f.MonthlyUSD, 32.85) || f.Confidence != "" || !strings.HasPrefix(f.Fix[0], "aws ec2 delete-nat-gateway --region eu-west-1 --nat-gateway-id nat-unrouted") {
		t.Errorf("nat-unrouted: %+v", f)
	}
	if f := got["nat-unrouted"]; f.Facts["nat"] != "nat-unrouted" || f.Facts["vpc"] != "vpc-b" {
		t.Errorf("facts must use idle-nat's names (nat, vpc), for the shared nat-delete script: %v", f.Facts)
	}
	if f := got["nat-fresh"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[0], "# ") {
		t.Errorf("a NAT made today may be about to get its routes: %+v", f)
	}
	r := fixture()
	r.NATRouted = nil // the route tables couldn't all be read
	if got := NATNoRoutes(r, opts()); got != nil {
		t.Errorf("must not guess without the route tables: %+v", got)
	}
}

func TestIdleClassicLBs(t *testing.T) {
	got := byResource(IdleClassicLBs(fixture(), opts()))
	if len(got) != 6 {
		t.Fatalf("want clb-empty, clb-down, clb-new, clb-asg, clb-k8s, clb-tags-unread; in service or unread health stay quiet: %+v", got)
	}
	if f := got["clb-empty"]; !f.Priced || !near(f.MonthlyUSD, 18.25) || f.Confidence != "" || f.Fix[1] != "aws elb delete-load-balancer --region eu-west-1 --load-balancer-name clb-empty" ||
		!strings.Contains(f.Fix[0], "clb-empty-1.eu-west-1.elb.amazonaws.com") || f.Facts["asgs"] != "" {
		t.Errorf("clb-empty: %+v", f)
	}
	if f := got["clb-down"]; f.Confidence != "medium" || !strings.HasPrefix(f.Fix[1], "# aws elb") {
		t.Errorf("instances out of service may be a deploy: %+v", f)
	}
	if f := got["clb-new"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[1], "# ") {
		t.Errorf("a load balancer made today may be about to get instances: %+v", f)
	}
	if f := got["clb-asg"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[1], "# ") || f.Facts["asgs"] != "web-a web-b" || !strings.Contains(f.Why, "web-a, web-b") {
		t.Errorf("an Auto Scaling group scaled to 0 empties it: %+v", f)
	}
	if f := got["clb-k8s"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[1], "# ") || !strings.Contains(f.Why, "Kubernetes cluster prod") {
		t.Errorf("a Kubernetes Service's load balancer empties with its nodes: %+v", f)
	}
	if f := got["clb-tags-unread"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[1], "# ") {
		t.Errorf("unread tags: no live delete: %+v", f)
	}
	r := fixture()
	r.ASGUnknown = true
	if f := byResource(IdleClassicLBs(r, opts()))["clb-empty"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[1], "# ") {
		t.Errorf("unread Auto Scaling groups: no live delete: %+v", f)
	}
}

// main's idle-load-balancer gets the same scale-up guard, through target groups.
func TestIdleLoadBalancersThatMayRefill(t *testing.T) {
	r := fixture()
	r.LoadBalancers = []inventory.LoadBalancer{
		{ARN: "arn:lb/asg", Name: "alb-asg", Type: "application", TargetGroups: 1, TargetGroupARNs: []string{"arn:tg/web"}},
		{ARN: "arn:lb/k8s", Name: "alb-k8s", Type: "application", TargetGroups: 1, TargetGroupARNs: []string{"arn:tg/k8s"}, Kubernetes: "?"},
		{ARN: "arn:lb/plain", Name: "alb-plain", Type: "application", TargetGroups: 1, TargetGroupARNs: []string{"arn:tg/plain"}},
	}
	r.ASGLoadBalancers = map[string][]string{"tg:arn:tg/web": {"web"}}
	got := byResource(IdleLoadBalancers(r, opts()))
	if f := got["alb-asg"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[0], "# ") || f.Facts["asgs"] != "web" {
		t.Errorf("alb-asg: %+v", f)
	}
	if f := got["alb-k8s"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[0], "# ") || !strings.HasPrefix(f.Why, "Kubernetes made it") {
		t.Errorf("alb-k8s: %+v", f)
	}
	if f := got["alb-plain"]; f.Confidence != "" || !strings.HasPrefix(f.Fix[0], "aws elbv2 delete-load-balancer") || f.Facts["asgs"] != "" {
		t.Errorf("alb-plain: %+v", f)
	}
}

// An ALB with no targets still answers users when a listener redirects or
// returns a fixed response: low; unread listeners: medium. Below high, the
// delete is a comment (as idle-classic-lb's).
func TestIdleLoadBalancersThatStillAnswer(t *testing.T) {
	r := fixture()
	r.LoadBalancers = []inventory.LoadBalancer{
		{ARN: "arn:lb/redirect", Name: "alb-redirect", Type: "application", Responds: 1},
		{ARN: "arn:lb/unread", Name: "alb-unread", Type: "application", ListenersUnknown: true},
		{ARN: "arn:lb/idle", Name: "alb-idle", Type: "application"},
	}
	got := byResource(IdleLoadBalancers(r, opts()))
	if f := got["alb-redirect"]; f.Confidence != "low" || !strings.HasPrefix(f.Fix[0], "# ") || !strings.Contains(f.Detail, "1 listener(s) redirect or return a fixed response") ||
		!strings.Contains(f.Why, "may still be in use") {
		t.Errorf("alb-redirect: %+v", f)
	}
	if f := got["alb-unread"]; f.Confidence != "medium" || len(f.Fix) != 2 || !strings.HasPrefix(f.Fix[0], "# a person's call: check its listeners") ||
		!strings.HasPrefix(f.Fix[1], "# aws elbv2 delete-load-balancer") || !strings.Contains(f.Detail, "couldn't be read") {
		t.Errorf("alb-unread: %+v", f)
	}
	if f := got["alb-idle"]; f.Confidence != "" || !strings.HasPrefix(f.Fix[0], "aws elbv2 delete-load-balancer") {
		t.Errorf("alb-idle: %+v", f)
	}
	// A low one's Fix says what a person checks before the commented delete,
	// and Evidence names the reads behind the confidence.
	ev := func(f Finding) string { return strings.Join(f.Evidence, "\n") }
	if f := got["alb-redirect"]; len(f.Fix) != 2 || !strings.HasPrefix(f.Fix[0], "# a person's call: check that nobody relies on its redirects") ||
		!strings.HasPrefix(f.Fix[1], "# aws elbv2 delete-load-balancer") ||
		!strings.Contains(ev(f), "ELB DescribeTargetGroups and DescribeTargetHealth: no target groups") ||
		!strings.Contains(ev(f), "ELB DescribeListeners and DescribeRules: 1 listener(s) with a redirect or fixed-response action") {
		t.Errorf("alb-redirect fix and evidence: %+v", f)
	}
	if f := got["alb-unread"]; !strings.Contains(ev(f), "DescribeListeners and DescribeRules: couldn't be read") {
		t.Errorf("alb-unread evidence: %+v", f)
	}
	if f := got["alb-idle"]; !strings.Contains(ev(f), "no listener redirects or returns a fixed response") {
		t.Errorf("alb-idle evidence: %+v", f)
	}
}

// An internet-facing ALB or NLB also pays for its public IPv4 addresses,
// which its hourly price leaves out; an internal one and a GWLB don't.
func TestIdleLoadBalancersPublicIPv4(t *testing.T) {
	r := fixture()
	r.LoadBalancers = []inventory.LoadBalancer{
		{ARN: "arn:lb/pub-alb", Name: "pub-alb", Type: "application", Scheme: "internet-facing"},
		{ARN: "arn:lb/pub-nlb", Name: "pub-nlb", Type: "network", Scheme: "internet-facing"},
		{ARN: "arn:lb/int-alb", Name: "int-alb", Type: "application", Scheme: "internal"},
		{ARN: "arn:lb/gwlb", Name: "gwlb", Type: "gateway"},
	}
	got := byResource(IdleLoadBalancers(r, opts()))
	const note = "it also pays $3.65 a month for each public IPv4 address (one per zone it's in), which this price leaves out"
	for _, name := range []string{"pub-alb", "pub-nlb"} {
		if !strings.Contains(got[name].Detail, note) {
			t.Errorf("%s: %q", name, got[name].Detail)
		}
	}
	for _, name := range []string{"int-alb", "gwlb"} {
		if strings.Contains(got[name].Detail, "IPv4") {
			t.Errorf("%s: %q", name, got[name].Detail)
		}
	}
}

func TestRDSOldClass(t *testing.T) {
	r := fixture()
	r.Usage = nil // free plan: no idle test
	got := byResource(RDSOldClass(r, opts()))
	want := map[string]float64{"old-m4": (0.175 - 0.152) * 730, "old-m4-57": (0.175 - 0.171) * 730, "old-t2": (0.018 - 0.016) * 730, "idle-old": (0.175 - 0.152) * 730}
	if f := got["old-m4"]; f.Facts["arn"] == "" || f.Facts["resource_id"] != "db-RES-old-m4" {
		t.Errorf("the IDs the hub may name it by: %v", f.Facts)
	}
	if f := got["old-m2"]; f.Priced || f.Confidence != "low" || !strings.Contains(f.Detail, "more than now") || !strings.HasPrefix(f.Why, "the current class of that size costs more") {
		t.Errorf("a costlier twin isn't a saving: %+v", f)
	}
	if f := got["old-t2"]; !strings.Contains(f.Why, "Unlimited") {
		t.Errorf("T3/T4g always run Unlimited on RDS: %q", f.Why)
	}
	for id, usd := range want {
		if f := got[id]; !f.Priced || !near(f.MonthlyUSD, usd) || !strings.HasPrefix(f.Fix[1], "# aws rds modify-db-instance") {
			t.Errorf("%s: want $%.2f: %+v", id, usd, f)
		}
	}
	if got["old-m4"].Facts["twin"] != "db.m6g.large" || got["old-m4"].Why == "" || got["old-m4-57"].Facts["twin"] != "db.m5.large" || got["old-t2"].Facts["twin"] != "db.t4g.micro" {
		t.Errorf("twins: Graviton only where the engine version runs on it: %v %v %v", got["old-m4"].Facts, got["old-m4-57"].Facts, got["old-t2"].Facts)
	}
	if f := got["old-m3-medium"]; f.Priced || !strings.Contains(f.Detail, "no current class has that size") {
		t.Errorf("old-m3-medium: %+v", f)
	}
	for _, id := range []string{"old-oracle", "member"} {
		if f := got[id]; f.Check == "" || f.Priced || f.Facts["twin"] != "db.r5.large" {
			t.Errorf("%s: an engine with no built-in price is named, unpriced: %+v", id, f)
		}
	}
	if len(got) != 8 {
		t.Errorf("current classes and a stopped database stay quiet: %d findings: %+v", len(got), got)
	}
}

func TestS3VersionsNoExpiry(t *testing.T) {
	got := byResource(S3VersionsNoExpiry(fixture(), opts()))
	if len(got) != 5 {
		t.Fatalf("want versions-kept, -suspended, -mfa, -locked and -lock-unread; quiet: one that expires old versions, unread versioning, never versioned: %+v", got)
	}
	f := got["versions-kept"]
	if f.Priced || f.Confidence != "" || f.Facts["has_rules"] != "false" || !strings.HasPrefix(f.Fix[1], "# aws s3api put-bucket-lifecycle-configuration --region eu-west-1 --bucket versions-kept") ||
		!strings.Contains(f.Fix[1], `"NoncurrentVersionExpiration":{"NoncurrentDays":30,"NewerNoncurrentVersions":3}`) {
		t.Errorf("versions-kept: unpriced, the expiring rule only as a comment: %+v", f)
	}
	if f := got["versions-suspended"]; !strings.Contains(f.Detail, "suspended") || f.Facts["versioning"] != "suspended" ||
		!strings.Contains(strings.Join(f.Fix, "\n"), "aws s3api get-bucket-lifecycle-configuration --region eu-west-1 --bucket versions-suspended") {
		t.Errorf("a bucket with rules: read them first (a put replaces them all): %+v", f)
	}
	for _, line := range got["versions-suspended"].Fix {
		if strings.HasPrefix(line, "aws s3api put-") {
			t.Errorf("never a live put on a bucket with rules: %q", line)
		}
	}
	if f := got["versions-mfa"]; f.Confidence != "low" || len(f.Fix) != 1 || !strings.Contains(f.Fix[0], "MFA Delete") {
		t.Errorf("MFA Delete: lifecycle rules don't run, so only a note: %+v", f)
	}
	for _, id := range []string{"versions-locked", "versions-lock-unread"} {
		if got[id].Confidence != "low" {
			t.Errorf("%s: Object Lock (or unread) is low: %+v", id, got[id])
		}
	}
}

func TestUnusedHealthChecks(t *testing.T) {
	got := byResource(UnusedHealthChecks(fixture(), opts()))
	if len(got) != 3 {
		t.Fatalf("want hc-unused, hc-basic, hc-watched; quiet: used, managed by a service: %+v", got)
	}
	if f := got["hc-unused"]; !f.Priced || !near(f.MonthlyUSD, 2) || f.Region != "global" || f.Confidence != "" || !strings.Contains(f.Detail, "api.example.com") ||
		f.Fix[1] != "# aws route53 delete-health-check --health-check-id hc-unused" {
		t.Errorf("hc-unused: two optional features at least: %+v", f)
	}
	if f := got["hc-basic"]; f.Priced || !strings.Contains(f.Detail, "free for an account's first 50") {
		t.Errorf("hc-basic: may be free (first 50 on AWS endpoints): unpriced: %+v", f)
	}
	if f := got["hc-watched"]; f.Confidence != "low" {
		t.Errorf("an alarm watches it: low: %+v", f)
	}
	r := fixture()
	r.HealthCheckUsesUnknown = true
	if got := UnusedHealthChecks(r, opts()); got != nil {
		t.Errorf("unread record sets: none is known unused: %+v", got)
	}
	r = fixture()
	r.HealthCheckAlarmsUnknown = true
	if f := byResource(UnusedHealthChecks(r, opts()))["hc-unused"]; f.Confidence != "low" {
		t.Errorf("unread alarms: low: %+v", f)
	}
	r = fixture()
	r.TrafficPolicies = true
	if f := byResource(UnusedHealthChecks(r, opts()))["hc-unused"]; f.Confidence != "low" || !strings.Contains(f.Why, "traffic policy") {
		t.Errorf("traffic policies may route on it: low: %+v", f)
	}
}

func TestRDSGraviton(t *testing.T) {
	r := fixture()
	r.Usage = nil // free plan: no idle test
	got := byResource(RDSGraviton(r, opts()))
	want := map[string]float64{"busy-db": (0.178 - 0.159) * 730, "silent-db": (0.178 - 0.159) * 730, "young-db": (0.171 - 0.152) * 730,
		"primary": (0.171 - 0.152) * 730, "replica": (0.171 - 0.152) * 730}
	for id, usd := range want {
		if f := got[id]; !f.Priced || !near(f.MonthlyUSD, usd) || f.Facts["twin"] != "db.m6g.large" || f.Facts["resource_id"] != "db-RES-"+id || !strings.HasPrefix(f.Fix[1], "# aws rds modify-db-instance") {
			t.Errorf("%s: want $%.2f against db.m6g.large: %+v", id, usd, f)
		}
	}
	if f := got["idle-db"]; f.Check != "rds-graviton" {
		t.Errorf("free plan: idle-db is a Graviton candidate: %+v", f)
	}
}

func TestRDSGravitonNamesAnOlderGeneration(t *testing.T) {
	r := fixture()
	r.Usage = nil
	r.DBInstances = []inventory.DBInstance{{ID: "m6i-db", ARN: "arn:aws:rds:eu-west-1:123456789012:db:m6i-db", Class: "db.m6i.large", Engine: "mysql", Version: "8.0.36", Status: "available", Created: ago(200)},
		{ID: "m5-db", ARN: "arn:aws:rds:eu-west-1:123456789012:db:m5-db", Class: "db.m5.large", Engine: "mysql", Version: "8.0.36", Status: "available", Created: ago(200)}}
	got := byResource(RDSGraviton(r, opts()))
	if f := got["m6i-db"]; !strings.Contains(f.Why, "db.m6g.large is Graviton2, a generation older than db.m6i") {
		t.Errorf("m6i-db: %q", f.Why)
	}
	if f := got["m5-db"]; f.Check == "" || f.Why != "" {
		t.Errorf("m5 to m6g is no step back: %+v", f)
	}
}

// ddbTestPrices: AWS's us-east-1 DynamoDB prices (offer file 2026-11-11).
func ddbTestPrices() map[string]float64 {
	return map[string]float64{"TimedStorage-ByteHrs": 0.25, "IA-TimedStorage-ByteHrs": 0.10,
		"ReadRequestUnits": 0.125e-6, "WriteRequestUnits": 0.625e-6, "IA-ReadRequestUnits": 0.155e-6, "IA-WriteRequestUnits": 0.78e-6,
		"ReadCapacityUnit-Hrs": 0.00013, "WriteCapacityUnit-Hrs": 0.00065, "IA-ReadCapacityUnit-Hrs": 0.00016, "IA-WriteCapacityUnit-Hrs": 0.00081}
}

// officeHours is four weeks hour by hour from a Monday 00:00 UTC, the last
// weeks of them with data: connections on weekdays 08:00 to 19:59 UTC, none
// at night or at the weekend. change edits an hour (week, hour of the week
// from Monday 00:00) before it's kept: false drops its datapoint.
func officeHours(weeks int, change func(week, hour int, conns, activity *float64) bool) *inventory.Hourly {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) // a Monday
	h := &inventory.Hourly{Start: start, Conns: map[int]float64{}, Activity: map[int]float64{}}
	for w := inventory.OffHoursWeeks - weeks; w < inventory.OffHoursWeeks; w++ {
		for s := 0; s < 168; s++ {
			day, hour := s/24, s%24
			conns, activity := 0.0, 0.0
			if day < 5 && hour >= 8 && hour < 20 {
				conns = 7
			}
			if change != nil && !change(w, s, &conns, &activity) {
				continue
			}
			h.Conns[w*168+s], h.Activity[w*168+s] = conns, activity
		}
	}
	return h
}

const auroraHours = inventory.OffHoursWeeks * 168

// auroraUse is four weeks by the hour: busy(i) says hour i had a connection.
func auroraUse(busy func(int) bool, acu float64) *inventory.Hourly {
	h := &inventory.Hourly{Conns: map[int]float64{}, Activity: map[int]float64{}, ACU: map[int]float64{}, ACUMin: map[int]float64{}}
	for i := range auroraHours {
		h.Conns[i], h.ACU[i], h.ACUMin[i] = 0, acu, acu
		if busy(i) {
			h.Conns[i] = 3
		}
	}
	return h
}

func dayBusy(i int) bool { return i%24 >= 12 } // idle the first 12 hours of every day
