package inventory

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"slices"
)

// ImageRefsAPI asks EC2 which resources reference an AMI. EC2 looks at
// instances, launch templates, SSM parameters and Image Builder recipes
// itself (aws:CalledVia ec2-images.amazonaws.com), so costhound never reads
// launch template user data or parameter values.
type ImageRefsAPI interface {
	ec2.DescribeImageReferencesAPIClient
}

// RouteTablesAPI lists route tables (what NAT gateways are routed from).
type RouteTablesAPI interface {
	ec2.DescribeRouteTablesAPIClient
}

// ASGAPI is the Auto Scaling call that tells whether launch configurations
// are in use (their AMI can't be read without their user data).
type ASGAPI interface {
	autoscaling.DescribeAutoScalingGroupsAPIClient
}

// KMSAPI is the KMS calls the key check uses.
type KMSAPI interface {
	kms.ListKeysAPIClient
	kms.ListAliasesAPIClient
	kms.ListKeyRotationsAPIClient
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

// RDSSnapshotAPI is the RDS calls that find snapshots of deleted databases.
type RDSSnapshotAPI interface {
	rds.DescribeDBInstancesAPIClient
	rds.DescribeDBClustersAPIClient
	rds.DescribeDBSnapshotsAPIClient
	rds.DescribeDBClusterSnapshotsAPIClient
}

// EKSFreeAPI is the EKS calls the empty-cluster check uses.
type EKSFreeAPI interface {
	eks.ListClustersAPIClient
	eks.ListNodegroupsAPIClient
	eks.ListFargateProfilesAPIClient
	eks.ListCapabilitiesAPIClient
	DescribeCluster(context.Context, *eks.DescribeClusterInput, ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

// EKSInfo is an EKS cluster and what it has to run pods on.
type EKSInfo struct {
	Name, Status, Version string
	VpcID                 string
	Tier                  string // provisioned control-plane tier ("standard", "tier-xl", ...)
	NodeGroups, Fargate   int    // managed node groups and Fargate profiles
	Capabilities          int    // EKS Capabilities (managed Argo CD, ACK, kro) run inside EKS, not on nodes
	AutoMode              bool   // EKS Auto Mode: EC2 hides its instances from DescribeInstances
	Hybrid                bool   // hybrid nodes run outside AWS, where costhound can't see them
	DeletionProtection    bool
	Created               *time.Time
}

// Host is an EC2 Dedicated Host.
type Host struct {
	ID, Family, Type, AZ, State string
	Reservation                 string // a Dedicated Host Reservation pays for it: releasing it saves nothing until that ends
	Owner                       string // hosts shared through RAM are listed in the consumer account too
	Instances                   int    // running instances only: stopped ones with host affinity aren't listed here
	Outpost                     string // a host on an Outpost: paid by the Outpost, not by the hour
	Allocated                   *time.Time
}

// KMSKey is a customer managed KMS key (AWS managed keys are free).
type KMSKey struct {
	ID, ARN, Alias, State string
	Rotations             int // completed rotations; each of the first two adds a billed key version
	Created               *time.Time
}

// DBSnapshot is a manual RDS snapshot (Cluster: an Aurora, DocumentDB or
// Neptune cluster snapshot) and whether the database it came from still exists.
type DBSnapshot struct {
	ID, Source, Engine string
	Cluster            bool
	GiB                int32
	Created            *time.Time
	SourceExists       bool // by name, or by resource ID (a renamed database keeps it)
	// Copy is a copy of another snapshot (from another region or account):
	// usually a disaster-recovery or backup copy of a database that lives on.
	Copy     bool
	CopyFrom string // the source region, if AWS says
	// The region still runs DB instances, whose free backup allowance (100% of
	// their storage) may cover this snapshot. Cluster snapshots of deleted
	// clusters are always billed (Aurora's allowance is per cluster).
	FreeAllowance bool
}

// loadLongTail reads the long-tail resources of the free checks.
func loadLongTail(ctx context.Context, r *Region, c Clients, skip func(string, error)) {
	if c.ASG != nil {
		p := autoscaling.NewDescribeAutoScalingGroupsPaginator(c.ASG, &autoscaling.DescribeAutoScalingGroupsInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *autoscaling.DescribeAutoScalingGroupsOutput) {
			for _, g := range o.AutoScalingGroups {
				if aws.ToString(g.LaunchConfigurationName) != "" {
					r.LaunchConfigASGs++
				}
				name := aws.ToString(g.AutoScalingGroupName)
				r.ASGNames = append(r.ASGNames, name)
				if r.ASGLaunch == nil {
					r.ASGLaunch = map[string]LaunchSource{}
				}
				lt := g.LaunchTemplate
				if lt == nil && g.MixedInstancesPolicy != nil && g.MixedInstancesPolicy.LaunchTemplate != nil {
					lt = g.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification
				}
				switch {
				case lt != nil:
					r.ASGLaunch[name] = LaunchSource{Template: aws.ToString(lt.LaunchTemplateName), TemplateID: aws.ToString(lt.LaunchTemplateId), Version: aws.ToString(lt.Version)}
				case aws.ToString(g.LaunchConfigurationName) != "":
					r.ASGLaunch[name] = LaunchSource{Config: aws.ToString(g.LaunchConfigurationName)}
				}
				for _, lb := range g.LoadBalancerNames {
					r.addASGLoadBalancer("clb:"+lb, name)
				}
				for _, tg := range g.TargetGroupARNs {
					r.addASGLoadBalancer("tg:"+tg, name)
				}
				// Attached with AttachTrafficSources: not in the two lists above.
				for _, ts := range g.TrafficSources {
					switch aws.ToString(ts.Type) {
					case "elb": // a Classic Load Balancer's name
						r.addASGLoadBalancer("clb:"+aws.ToString(ts.Identifier), name)
					case "elbv2": // an ALB, NLB or GWLB target group's ARN
						r.addASGLoadBalancer("tg:"+aws.ToString(ts.Identifier), name)
					}
				}
				for _, tg := range g.Tags {
					k, v := aws.ToString(tg.Key), aws.ToString(tg.Value)
					name, ok := strings.CutPrefix(k, "kubernetes.io/cluster/")
					if !ok {
						name, ok = strings.CutPrefix(k, "k8s.io/cluster-autoscaler/")
					}
					if !ok && k == "eks:cluster-name" {
						name, ok = v, true
					}
					if ok && name != "" && name != "enabled" {
						if r.EKSAutoScaling == nil {
							r.EKSAutoScaling = map[string]int{}
						}
						r.EKSAutoScaling[name]++
					}
				}
			}
		}); err != nil {
			skip("autoscaling:DescribeAutoScalingGroups", err)
			r.LaunchConfigASGs = -1 // unknown
			r.ASGUnknown = true
		} else {
			r.exists("AutoScalingGroupName", r.ASGNames)
		}
	}
	if c.Hosts != nil {
		p := ec2.NewDescribeHostsPaginator(c.Hosts, &ec2.DescribeHostsInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeHostsOutput) {
			for _, h := range o.Hosts {
				st := string(h.State)
				if st == "released" || st == "released-permanent-failure" {
					continue // no longer billed
				}
				host := Host{ID: aws.ToString(h.HostId), AZ: aws.ToString(h.AvailabilityZone), State: st, Owner: aws.ToString(h.OwnerId),
					Reservation: aws.ToString(h.HostReservationId), Instances: len(h.Instances), Outpost: aws.ToString(h.OutpostArn), Allocated: h.AllocationTime}
				if hp := h.HostProperties; hp != nil {
					host.Family, host.Type = aws.ToString(hp.InstanceFamily), aws.ToString(hp.InstanceType)
				}
				if host.Family == "" && host.Type != "" {
					host.Family, _, _ = strings.Cut(host.Type, ".")
				}
				r.Hosts = append(r.Hosts, host)
			}
		}); err != nil {
			skip("ec2:DescribeHosts", err)
		}
	}
	if c.Offerings != nil {
		loadOfferings(ctx, r, c.Offerings, skip)
	}
	if c.KMS != nil {
		if what, err := loadKeys(ctx, r, c.KMS); err != nil {
			skip(what, err)
		}
	}
	if c.RDS != nil {
		if what, err := loadDBSnapshots(ctx, r, c.RDS); err != nil {
			skip(what, err)
		}
	}
	if c.EKS != nil {
		if what, err := loadEKS(ctx, r, c.EKS); err != nil {
			skip(what, err)
		}
	}
	if c.Classic != nil {
		loadClassicLBs(ctx, r, c.Classic, skip)
	}
	if c.APIGateway != nil {
		loadRestStages(ctx, r, c.APIGateway, skip)
	}
	if c.CloudTrail != nil {
		loadTrails(ctx, r, c.CloudTrail, skip)
	}
	if c.Routes != nil {
		// Every route target: "nat:" NAT gateway, "eni:" network interface,
		// "ip:<vpc>/" next-hop address (VPC Route Server; an address is only
		// unique inside its VPC). A route to a VPC endpoint (a Gateway Load
		// Balancer endpoint, or a gateway endpoint's prefix list) names it as
		// GatewayId. nil (unknown) if any page fails.
		routed, viaEndpoint := map[string]bool{}, map[string][]string{}
		p := ec2.NewDescribeRouteTablesPaginator(c.Routes, &ec2.DescribeRouteTablesInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeRouteTablesOutput) {
			for _, rt := range o.RouteTables {
				for _, route := range rt.Routes {
					if id := aws.ToString(route.NatGatewayId); id != "" {
						routed["nat:"+id] = true
					}
					if id := aws.ToString(route.NetworkInterfaceId); id != "" {
						routed["eni:"+id] = true
					}
					if ip := aws.ToString(route.IpAddress); ip != "" {
						routed["ip:"+aws.ToString(rt.VpcId)+"/"+ip] = true
					}
					if id := aws.ToString(route.GatewayId); strings.HasPrefix(id, "vpce-") {
						dest := cmp.Or(aws.ToString(route.DestinationCidrBlock), aws.ToString(route.DestinationIpv6CidrBlock), aws.ToString(route.DestinationPrefixListId))
						viaEndpoint[id] = append(viaEndpoint[id], strings.TrimSpace(aws.ToString(rt.RouteTableId)+" "+dest))
					}
				}
			}
		}); err != nil {
			skip("ec2:DescribeRouteTables", err)
		} else {
			r.NATRouted, r.EndpointRoutes = routed, viaEndpoint
		}
	}
}

// imageRefBatch is DescribeImageReferences' limit of image IDs per call.
const imageRefBatch = 10

// LoadImageRefs records which of the region's own AMIs something still
// references: a non-terminated instance, any of a launch template's last
// 10,000 versions, an SSM parameter or an Image Builder recipe. Called after
// the owner filter, on AMIs no instance in the inventory uses. On any error
// the whole set is unknown (nil): the AMI check must not run on a guess.
func loadImageRefs(ctx context.Context, r *Region, c ImageRefsAPI) error {
	inUse := map[string]bool{}
	for _, in := range r.Instances {
		if in.State == nil || in.State.Name != "terminated" {
			inUse[aws.ToString(in.ImageId)] = true
		}
	}
	var ids []string
	for _, img := range r.Images {
		if id := aws.ToString(img.ImageId); !inUse[id] {
			ids = append(ids, id)
		}
	}
	refs := map[string]bool{}
	for i := 0; i < len(ids); i += imageRefBatch {
		in := &ec2.DescribeImageReferencesInput{
			ImageIds:                ids[i:min(i+imageRefBatch, len(ids))],
			IncludeAllResourceTypes: aws.Bool(true),
			ResourceTypes: []ec2types.ResourceTypeRequest{
				{ResourceType: "ec2:Instance", ResourceTypeOptions: []ec2types.ResourceTypeOption{
					{OptionName: "state-name", OptionValues: []string{"pending", "running", "shutting-down", "stopping", "stopped"}}}},
				{ResourceType: "ec2:LaunchTemplate", ResourceTypeOptions: []ec2types.ResourceTypeOption{
					{OptionName: "version-depth", OptionValues: []string{"10000"}}}}, // every version: a group may pin an old one
			},
		}
		p := ec2.NewDescribeImageReferencesPaginator(c, in)
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeImageReferencesOutput) {
			for _, ref := range o.ImageReferences {
				refs[aws.ToString(ref.ImageId)] = true
			}
		}); err != nil {
			return err
		}
	}
	r.ImageRefs = refs
	return nil
}

// loadKeys lists customer managed KMS keys with their state, alias and, for
// disabled keys, how many times they were rotated. AWS managed keys
// (alias/aws/...) are free and skipped without a DescribeKey call.
func loadKeys(ctx context.Context, r *Region, c KMSAPI) (string, error) {
	alias := map[string]string{}
	awsManaged := map[string]bool{}
	ap := kms.NewListAliasesPaginator(c, &kms.ListAliasesInput{})
	if err := drain(ctx, ap.HasMorePages, ap.NextPage, func(o *kms.ListAliasesOutput) {
		for _, a := range o.Aliases {
			id, name := aws.ToString(a.TargetKeyId), aws.ToString(a.AliasName)
			if id == "" {
				continue
			}
			if strings.HasPrefix(name, "alias/aws/") {
				awsManaged[id] = true
			} else if alias[id] == "" {
				alias[id] = name
			}
		}
	}); err != nil {
		return "kms:ListAliases", err
	}
	var ids []string
	kp := kms.NewListKeysPaginator(c, &kms.ListKeysInput{})
	if err := drain(ctx, kp.HasMorePages, kp.NextPage, func(o *kms.ListKeysOutput) {
		for _, k := range o.Keys {
			if id := aws.ToString(k.KeyId); !awsManaged[id] {
				ids = append(ids, id)
			}
		}
	}); err != nil {
		return "kms:ListKeys", err
	}
	for _, id := range ids {
		d, err := c.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(id)})
		if err != nil || d.KeyMetadata == nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "kms:DescribeKey", Error: ShortError(err)})
			continue
		}
		m := d.KeyMetadata
		if m.KeyManager != kmstypes.KeyManagerTypeCustomer {
			continue
		}
		k := KMSKey{ID: id, ARN: aws.ToString(m.Arn), Alias: alias[id], State: string(m.KeyState), Created: m.CreationDate}
		if m.KeyState == kmstypes.KeyStateDisabled {
			rp := kms.NewListKeyRotationsPaginator(c, &kms.ListKeyRotationsInput{KeyId: aws.String(id)})
			if err := drain(ctx, rp.HasMorePages, rp.NextPage, func(o *kms.ListKeyRotationsOutput) {
				k.Rotations += len(o.Rotations)
			}); err != nil {
				var unsupported *kmstypes.UnsupportedOperationException
				if !errors.As(err, &unsupported) { // keys that can't rotate have no rotations
					r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "kms:ListKeyRotations", Error: ShortError(err)})
				}
				k.Rotations = 0
			}
		}
		r.Keys = append(r.Keys, k)
	}
	return "", nil
}

// DBInstance is an RDS DB instance as the RDS checks need it.
type DBInstance struct {
	ID, ARN, ResourceID         string // ResourceID: the DbiResourceId, which a rename keeps
	Class, Engine, Version      string
	Status, License             string
	Cluster                     string // the Aurora or Multi-AZ DB cluster it belongs to, "" if none
	ReplicaOf                   string // what a read replica replicates from, "" if it isn't one
	Replicas                    int    // read replicas (instances and clusters) replicating from it
	MultiAZ, DeletionProtection bool
	StorageType                 string
	GiB                         int
	Created                     *time.Time
}

// LaunchSource is what launches an Auto Scaling group's instances: a launch
// template (name, ID, version) or a launch configuration.
type LaunchSource struct {
	Template, TemplateID, Version string
	Config                        string
}

// DBCluster is an RDS DB cluster as aurora-serverless-pause needs it.
type DBCluster struct {
	ID, ARN, ResourceID string
	Engine, Version     string
	Status, StorageType string // StorageType "aurora-iopt1" is I/O-Optimized
	Global              string // the global database it's a member of, "" if none
	DataAPI             bool   // the RDS Data API is on: its requests wake the writer
	ActivityStream      bool   // a database activity stream is started: the writer can't pause
	ServerlessV2        bool   // it has a Serverless v2 capacity range
	MinACU, MaxACU      float64
	PauseAfter          int // SecondsUntilAutoPause; set only while MinACU is 0
	Members             []DBClusterMember
	Created             *time.Time
}

// DBClusterMember is a DB instance in a cluster.
type DBClusterMember struct {
	ID     string
	Writer bool
	Tier   int // failover promotion tier, 0-15
}

// loadDBSnapshots lists manual DB and DB cluster snapshots and marks the ones
// whose database no longer exists. If the live databases can't be listed,
// nothing is loaded: every snapshot would look orphaned.
func loadDBSnapshots(ctx context.Context, r *Region, c RDSSnapshotAPI) (string, error) {
	live := map[string]bool{} // "db:name", "cluster:name", and the resource IDs a rename keeps
	instances := 0            // live DB instances, whose storage sets a free backup allowance
	var dbs []DBInstance
	ip := rds.NewDescribeDBInstancesPaginator(c, &rds.DescribeDBInstancesInput{})
	if err := drain(ctx, ip.HasMorePages, ip.NextPage, func(o *rds.DescribeDBInstancesOutput) {
		for _, d := range o.DBInstances {
			replicaOf := aws.ToString(d.ReadReplicaSourceDBInstanceIdentifier)
			if replicaOf == "" {
				replicaOf = aws.ToString(d.ReadReplicaSourceDBClusterIdentifier)
			}
			dbs = append(dbs, DBInstance{ID: aws.ToString(d.DBInstanceIdentifier), ARN: aws.ToString(d.DBInstanceArn), ResourceID: aws.ToString(d.DbiResourceId),
				Class: aws.ToString(d.DBInstanceClass), Engine: aws.ToString(d.Engine), Version: aws.ToString(d.EngineVersion),
				Status: aws.ToString(d.DBInstanceStatus), License: aws.ToString(d.LicenseModel), Cluster: aws.ToString(d.DBClusterIdentifier),
				ReplicaOf: replicaOf, Replicas: len(d.ReadReplicaDBInstanceIdentifiers) + len(d.ReadReplicaDBClusterIdentifiers),
				MultiAZ: aws.ToBool(d.MultiAZ), DeletionProtection: aws.ToBool(d.DeletionProtection),
				StorageType: aws.ToString(d.StorageType), GiB: int(aws.ToInt32(d.AllocatedStorage)), Created: d.InstanceCreateTime})
			live["db:"+aws.ToString(d.DBInstanceIdentifier)] = true
			live["dbi:"+aws.ToString(d.DbiResourceId)] = aws.ToString(d.DbiResourceId) != ""
			if aws.ToString(d.DBClusterIdentifier) == "" { // cluster members count under their cluster
				instances++
			}
		}
	}); err != nil {
		return "rds:DescribeDBInstances", err
	}
	r.DBInstances = dbs // the whole list was read, whatever fails below
	r.exists("DBInstanceIdentifier", liveIDs(dbs, func(d DBInstance) (string, bool) { return d.ID, d.Status != "deleting" }))
	clusters := []DBCluster{} // read, even when empty
	cp := rds.NewDescribeDBClustersPaginator(c, &rds.DescribeDBClustersInput{})
	if err := drain(ctx, cp.HasMorePages, cp.NextPage, func(o *rds.DescribeDBClustersOutput) {
		for _, d := range o.DBClusters {
			live["cluster:"+aws.ToString(d.DBClusterIdentifier)] = true
			live["cluster-res:"+aws.ToString(d.DbClusterResourceId)] = aws.ToString(d.DbClusterResourceId) != ""
			x := DBCluster{ID: aws.ToString(d.DBClusterIdentifier), ARN: aws.ToString(d.DBClusterArn), ResourceID: aws.ToString(d.DbClusterResourceId),
				Engine: aws.ToString(d.Engine), Version: aws.ToString(d.EngineVersion), Status: aws.ToString(d.Status), StorageType: aws.ToString(d.StorageType),
				Global: aws.ToString(d.GlobalClusterIdentifier), DataAPI: aws.ToBool(d.HttpEndpointEnabled), Created: d.ClusterCreateTime,
				ActivityStream: d.ActivityStreamStatus == rdstypes.ActivityStreamStatusStarted || d.ActivityStreamStatus == rdstypes.ActivityStreamStatusStarting}
			if s := d.ServerlessV2ScalingConfiguration; s != nil && s.MaxCapacity != nil {
				x.ServerlessV2, x.MinACU, x.MaxACU, x.PauseAfter = true, aws.ToFloat64(s.MinCapacity), aws.ToFloat64(s.MaxCapacity), int(aws.ToInt32(s.SecondsUntilAutoPause))
			}
			for _, m := range d.DBClusterMembers {
				x.Members = append(x.Members, DBClusterMember{ID: aws.ToString(m.DBInstanceIdentifier), Writer: aws.ToBool(m.IsClusterWriter), Tier: int(aws.ToInt32(m.PromotionTier))})
			}
			clusters = append(clusters, x)
		}
	}); err != nil {
		return "rds:DescribeDBClusters", err
	}
	r.DBClusters = clusters
	sp := rds.NewDescribeDBSnapshotsPaginator(c, &rds.DescribeDBSnapshotsInput{SnapshotType: aws.String("manual")})
	if err := drain(ctx, sp.HasMorePages, sp.NextPage, func(o *rds.DescribeDBSnapshotsOutput) {
		for _, s := range o.DBSnapshots {
			// Filtered here too: not every RDS-compatible endpoint honors SnapshotType.
			if aws.ToString(s.SnapshotType) != "manual" || aws.ToString(s.Status) != "available" {
				continue
			}
			src, region := aws.ToString(s.DBInstanceIdentifier), aws.ToString(s.SourceRegion)
			r.DBSnapshots = append(r.DBSnapshots, DBSnapshot{ID: aws.ToString(s.DBSnapshotIdentifier), Source: src, Engine: aws.ToString(s.Engine),
				GiB: aws.ToInt32(s.AllocatedStorage), Created: s.SnapshotCreateTime,
				SourceExists: live["db:"+src] || live["dbi:"+aws.ToString(s.DbiResourceId)],
				Copy:         aws.ToString(s.SourceDBSnapshotIdentifier) != "" || (region != "" && region != r.Name), CopyFrom: region,
				FreeAllowance: instances > 0})
		}
	}); err != nil {
		return "rds:DescribeDBSnapshots", err
	}
	csp := rds.NewDescribeDBClusterSnapshotsPaginator(c, &rds.DescribeDBClusterSnapshotsInput{SnapshotType: aws.String("manual")})
	if err := drain(ctx, csp.HasMorePages, csp.NextPage, func(o *rds.DescribeDBClusterSnapshotsOutput) {
		for _, s := range o.DBClusterSnapshots {
			if aws.ToString(s.SnapshotType) != "manual" || aws.ToString(s.Status) != "available" {
				continue
			}
			src := aws.ToString(s.DBClusterIdentifier)
			r.DBSnapshots = append(r.DBSnapshots, DBSnapshot{ID: aws.ToString(s.DBClusterSnapshotIdentifier), Source: src, Engine: aws.ToString(s.Engine),
				Cluster: true, GiB: aws.ToInt32(s.AllocatedStorage), Created: s.SnapshotCreateTime,
				SourceExists: live["cluster:"+src] || live["cluster-res:"+aws.ToString(s.DbClusterResourceId)],
				Copy:         aws.ToString(s.SourceDBClusterSnapshotArn) != ""})
		}
	}); err != nil {
		return "rds:DescribeDBClusterSnapshots", err
	}
	return "", nil
}

// loadEKS lists EKS clusters with their node groups and Fargate profiles.
// A cluster whose node groups or profiles can't be listed is left out: it
// would look empty.
func loadEKS(ctx context.Context, r *Region, c EKSFreeAPI) (string, error) {
	var names []string
	p := eks.NewListClustersPaginator(c, &eks.ListClustersInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *eks.ListClustersOutput) {
		names = append(names, o.Clusters...)
	}); err != nil {
		return "eks:ListClusters", err
	}
	// Every name, before any detail read can fail: a resource tagged for a
	// cluster that isn't here is left behind; one whose details failed isn't.
	r.EKSListed, r.EKSNames = true, append([]string{}, names...)
	for _, n := range names {
		d, err := c.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(n)})
		if err != nil || d.Cluster == nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "eks:DescribeCluster", Error: ShortError(err)})
			continue
		}
		cl := d.Cluster
		info := EKSInfo{Name: n, Status: string(cl.Status), Version: aws.ToString(cl.Version), Created: cl.CreatedAt,
			AutoMode: cl.ComputeConfig != nil && aws.ToBool(cl.ComputeConfig.Enabled), Hybrid: cl.RemoteNetworkConfig != nil,
			DeletionProtection: aws.ToBool(cl.DeletionProtection), Tier: "standard"}
		if v := cl.ResourcesVpcConfig; v != nil {
			info.VpcID = aws.ToString(v.VpcId)
		}
		if s := cl.ControlPlaneScalingConfig; s != nil && s.Tier != "" {
			info.Tier = string(s.Tier)
		}
		ng := eks.NewListNodegroupsPaginator(c, &eks.ListNodegroupsInput{ClusterName: aws.String(n)})
		if err := drain(ctx, ng.HasMorePages, ng.NextPage, func(o *eks.ListNodegroupsOutput) { info.NodeGroups += len(o.Nodegroups) }); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "eks:ListNodegroups", Error: ShortError(err)})
			continue
		}
		fp := eks.NewListFargateProfilesPaginator(c, &eks.ListFargateProfilesInput{ClusterName: aws.String(n)})
		if err := drain(ctx, fp.HasMorePages, fp.NextPage, func(o *eks.ListFargateProfilesOutput) { info.Fargate += len(o.FargateProfileNames) }); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "eks:ListFargateProfiles", Error: ShortError(err)})
			continue
		}
		cp := eks.NewListCapabilitiesPaginator(c, &eks.ListCapabilitiesInput{ClusterName: aws.String(n)})
		if err := drain(ctx, cp.HasMorePages, cp.NextPage, func(o *eks.ListCapabilitiesOutput) { info.Capabilities += len(o.Capabilities) }); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "eks:ListCapabilities", Error: ShortError(err)})
			continue
		}
		r.EKSClusters = append(r.EKSClusters, info)
	}
	return "", nil
}

func (r *Region) addASGLoadBalancer(key, asg string) {
	if r.ASGLoadBalancers == nil {
		r.ASGLoadBalancers = map[string][]string{}
	}
	if !slices.Contains(r.ASGLoadBalancers[key], asg) { // listed and attached as a traffic source
		r.ASGLoadBalancers[key] = append(r.ASGLoadBalancers[key], asg)
	}
}
