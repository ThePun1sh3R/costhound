package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeImageRefs struct {
	calls int
	fail  bool
}

func (f *fakeImageRefs) DescribeImageReferences(_ context.Context, in *ec2.DescribeImageReferencesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImageReferencesOutput, error) {
	f.calls++
	if f.fail {
		return nil, &smithy.GenericAPIError{Code: "UnauthorizedOperation"}
	}
	if len(in.ImageIds) > 10 {
		return nil, &smithy.GenericAPIError{Code: "InvalidParameterValue", Message: "at most 10 image IDs"}
	}
	var states, depth []string
	for _, rt := range in.ResourceTypes {
		for _, o := range rt.ResourceTypeOptions {
			if o.OptionName == "state-name" {
				states = o.OptionValues
			} else if o.OptionName == "version-depth" {
				depth = o.OptionValues
			}
		}
	}
	for _, s := range states {
		if s == "terminated" {
			return nil, fmt.Errorf("a terminated instance must not count as a reference")
		}
	}
	if len(depth) != 1 || depth[0] != "10000" || !aws.ToBool(in.IncludeAllResourceTypes) {
		return nil, fmt.Errorf("want every resource type and every launch template version, got %v", depth)
	}
	out := &ec2.DescribeImageReferencesOutput{}
	for _, id := range in.ImageIds {
		if id == "ami-in-template" || id == "ami-in-ssm" {
			out.ImageReferences = append(out.ImageReferences, ec2types.ImageReference{ImageId: aws.String(id), ResourceType: "ec2:LaunchTemplate"})
		}
	}
	return out, nil
}

func TestLoadImageRefs(t *testing.T) {
	r := &Region{Name: "eu-west-1", Instances: []ec2types.Instance{{ImageId: aws.String("ami-running"), State: &ec2types.InstanceState{Name: "running"}}}}
	for i := 0; i < 12; i++ {
		r.Images = append(r.Images, ec2types.Image{ImageId: aws.String(fmt.Sprintf("ami-%02d", i))})
	}
	r.Images = append(r.Images, ec2types.Image{ImageId: aws.String("ami-in-template")}, ec2types.Image{ImageId: aws.String("ami-running")})
	f := &fakeImageRefs{}
	if err := loadImageRefs(context.Background(), r, f); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 || !r.ImageRefs["ami-in-template"] || len(r.ImageRefs) != 1 {
		t.Fatalf("want 13 AMIs (the running one skipped) in 2 calls of at most 10, got %d calls, refs %v", f.calls, r.ImageRefs)
	}
	r.ImageRefs = nil
	if err := loadImageRefs(context.Background(), r, &fakeImageRefs{fail: true}); err == nil || r.ImageRefs != nil {
		t.Fatalf("a failed lookup leaves references unknown (nil): %v %v", err, r.ImageRefs)
	}
}

type fakeASG struct{}

func (fakeASG) DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	tag := func(k, v string) astypes.TagDescription {
		return astypes.TagDescription{Key: aws.String(k), Value: aws.String(v)}
	}
	return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{
		{AutoScalingGroupName: aws.String("old"), LaunchConfigurationName: aws.String("lc-1"), LoadBalancerNames: []string{"clb-web"}},
		{AutoScalingGroupName: aws.String("new"), LoadBalancerNames: []string{"clb-web"}, TargetGroupARNs: []string{"arn:tg/web"},
			TrafficSources: []astypes.TrafficSourceIdentifier{{Type: aws.String("elbv2"), Identifier: aws.String("arn:tg/web")}}}, // listed twice: once
		{AutoScalingGroupName: aws.String("ts-only"), TrafficSources: []astypes.TrafficSourceIdentifier{ // attached only as traffic sources
			{Type: aws.String("elb"), Identifier: aws.String("clb-ts")}, {Type: aws.String("elbv2"), Identifier: aws.String("arn:tg/ts")},
			{Type: aws.String("vpc-lattice"), Identifier: aws.String("arn:aws:vpc-lattice:eu-west-1:111111111111:targetgroup/tg-1")}}},
		{AutoScalingGroupName: aws.String("nodes-a"), Tags: []astypes.TagDescription{tag("kubernetes.io/cluster/dev", "owned"), tag("k8s.io/cluster-autoscaler/enabled", "true")}},
		{AutoScalingGroupName: aws.String("nodes-b"), Tags: []astypes.TagDescription{tag("k8s.io/cluster-autoscaler/stage", "owned")}},
		{AutoScalingGroupName: aws.String("nodes-c"), Tags: []astypes.TagDescription{tag("eks:cluster-name", "prod")}},
	}}, nil
}

type deniedASG struct{}

func (deniedASG) DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
}

func TestASGReadFailureIsUnknown(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, ASG: deniedASG{}})
	skipped := false
	for _, s := range r.Skipped {
		skipped = skipped || s.What == "autoscaling:DescribeAutoScalingGroups"
	}
	if r.LaunchConfigASGs != -1 || !r.ASGUnknown || !skipped {
		t.Fatalf("a failed Auto Scaling read must be unknown (-1) and a skip: %d %v", r.LaunchConfigASGs, r.Skipped)
	}
}

func TestLoadASGsAndHostOwners(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, ASG: fakeASG{}, Hosts: fakeHosts{}})
	if r.LaunchConfigASGs != 1 {
		t.Errorf("want 1 group on a launch configuration, got %d", r.LaunchConfigASGs)
	}
	if strings.Join(r.ASGLoadBalancers["clb:clb-web"], " ") != "old new" || strings.Join(r.ASGLoadBalancers["tg:arn:tg/web"], " ") != "new" || r.ASGUnknown {
		t.Errorf("what each group registers instances with: %v", r.ASGLoadBalancers)
	}
	if strings.Join(r.ASGLoadBalancers["clb:clb-ts"], " ") != "ts-only" || strings.Join(r.ASGLoadBalancers["tg:arn:tg/ts"], " ") != "ts-only" || len(r.ASGLoadBalancers) != 4 {
		t.Errorf("traffic sources (elb: a Classic name, elbv2: a target group; not VPC Lattice): %v", r.ASGLoadBalancers)
	}
	if r.EKSAutoScaling["dev"] != 1 || r.EKSAutoScaling["stage"] != 1 || r.EKSAutoScaling["prod"] != 1 || len(r.EKSAutoScaling) != 3 {
		t.Errorf("node-group ASGs by all three tags (not the autoscaler's 'enabled' flag): %v", r.EKSAutoScaling)
	}
	for _, h := range r.Hosts {
		if h.ID == "h-shared" {
			t.Error("a host shared through RAM by another account must be dropped")
		}
	}
}

type fakeKMS struct{ rotationsErr error }

func (fakeKMS) ListAliases(context.Context, *kms.ListAliasesInput, ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	return &kms.ListAliasesOutput{Aliases: []kmstypes.AliasListEntry{
		{AliasName: aws.String("alias/aws/ebs"), TargetKeyId: aws.String("k-aws")},
		{AliasName: aws.String("alias/billing"), TargetKeyId: aws.String("k-off")},
		{AliasName: aws.String("alias/unused-alias")}, // points at no key
	}}, nil
}

func (fakeKMS) ListKeys(context.Context, *kms.ListKeysInput, ...func(*kms.Options)) (*kms.ListKeysOutput, error) {
	return &kms.ListKeysOutput{Keys: []kmstypes.KeyListEntry{{KeyId: aws.String("k-aws")}, {KeyId: aws.String("k-off")}, {KeyId: aws.String("k-on")}, {KeyId: aws.String("k-svc")}}}, nil
}

func (fakeKMS) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	m := &kmstypes.KeyMetadata{KeyId: in.KeyId, Arn: aws.String("arn:aws:kms:eu-west-1:111111111111:key/" + aws.ToString(in.KeyId)), KeyManager: kmstypes.KeyManagerTypeCustomer, KeyState: kmstypes.KeyStateEnabled}
	switch aws.ToString(in.KeyId) {
	case "k-aws":
		panic("AWS managed keys must be skipped by alias, without a DescribeKey call")
	case "k-off":
		m.KeyState = kmstypes.KeyStateDisabled
	case "k-svc":
		m.KeyManager = kmstypes.KeyManagerTypeAws // an AWS managed key with no alias listed
	}
	return &kms.DescribeKeyOutput{KeyMetadata: m}, nil
}

func (f fakeKMS) ListKeyRotations(context.Context, *kms.ListKeyRotationsInput, ...func(*kms.Options)) (*kms.ListKeyRotationsOutput, error) {
	if f.rotationsErr != nil {
		return nil, f.rotationsErr
	}
	return &kms.ListKeyRotationsOutput{Rotations: []kmstypes.RotationsListEntry{{}, {}, {}}}, nil
}

func TestLoadKeys(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	if _, err := loadKeys(context.Background(), r, fakeKMS{}); err != nil {
		t.Fatal(err)
	}
	if len(r.Keys) != 2 || r.Keys[0].ID != "k-off" || r.Keys[0].Alias != "alias/billing" || r.Keys[0].Rotations != 3 || r.Keys[1].ID != "k-on" || r.Keys[1].Rotations != 0 {
		t.Fatalf("got %+v, want k-off (aliased, 3 rotations) and k-on", r.Keys)
	}
	r = &Region{Name: "eu-west-1"}
	if _, err := loadKeys(context.Background(), r, fakeKMS{rotationsErr: &kmstypes.UnsupportedOperationException{}}); err != nil || len(r.Skipped) != 0 {
		t.Fatalf("keys that can't rotate are not a skip: %v %v", err, r.Skipped)
	}
	r = &Region{Name: "eu-west-1"}
	if _, err := loadKeys(context.Background(), r, fakeKMS{rotationsErr: &smithy.GenericAPIError{Code: "AccessDeniedException"}}); err != nil || len(r.Skipped) != 1 || r.Keys[0].Rotations != 0 {
		t.Fatalf("a denied rotation list is a skip, priced at one version: %v %v %+v", err, r.Skipped, r.Keys)
	}
}

type fakeRDSSnaps struct{ noInstances bool }

func (f fakeRDSSnaps) DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	out := []rdstypes.DBInstance{{DBInstanceIdentifier: aws.String("aurora-1"), DBClusterIdentifier: aws.String("live-cluster")}}
	if !f.noInstances {
		out = append(out, rdstypes.DBInstance{DBInstanceIdentifier: aws.String("live-db"), DbiResourceId: aws.String("db-RES1")})
	}
	return &rds.DescribeDBInstancesOutput{DBInstances: out}, nil
}

func (fakeRDSSnaps) DescribeDBClusters(context.Context, *rds.DescribeDBClustersInput, ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error) {
	sv2 := rdstypes.DBCluster{DBClusterIdentifier: aws.String("sv2"), Engine: aws.String("aurora-mysql"), EngineVersion: aws.String("8.0.mysql_aurora.3.08.0"),
		Status: aws.String("available"), StorageType: aws.String("aurora-iopt1"), GlobalClusterIdentifier: aws.String("g-1"), HttpEndpointEnabled: aws.Bool(true),
		ActivityStreamStatus:             rdstypes.ActivityStreamStatusStarted,
		ServerlessV2ScalingConfiguration: &rdstypes.ServerlessV2ScalingConfigurationInfo{MinCapacity: aws.Float64(0), MaxCapacity: aws.Float64(8), SecondsUntilAutoPause: aws.Int32(600)},
		DBClusterMembers: []rdstypes.DBClusterMember{{DBInstanceIdentifier: aws.String("sv2-w"), IsClusterWriter: aws.Bool(true), PromotionTier: aws.Int32(1)},
			{DBInstanceIdentifier: aws.String("sv2-r"), IsClusterWriter: aws.Bool(false), PromotionTier: aws.Int32(2)}}}
	return &rds.DescribeDBClustersOutput{DBClusters: []rdstypes.DBCluster{{DBClusterIdentifier: aws.String("live-cluster")}, sv2}}, nil
}

func (fakeRDSSnaps) DescribeDBSnapshots(context.Context, *rds.DescribeDBSnapshotsInput, ...func(*rds.Options)) (*rds.DescribeDBSnapshotsOutput, error) {
	s := func(id, src, typ string) rdstypes.DBSnapshot {
		return rdstypes.DBSnapshot{DBSnapshotIdentifier: aws.String(id), DBInstanceIdentifier: aws.String(src), SnapshotType: aws.String(typ), Status: aws.String("available"), AllocatedStorage: aws.Int32(20)}
	}
	renamed := s("d", "old-name", "manual")
	renamed.DbiResourceId = aws.String("db-RES1") // the live database was renamed: same resource ID
	copied := s("e", "far-db", "manual")
	copied.SourceDBSnapshotIdentifier, copied.SourceRegion = aws.String("arn:aws:rds:us-east-1:1:snapshot:x"), aws.String("us-east-1")
	regionOnly := s("f", "far-db2", "manual") // only SourceRegion says it came from elsewhere
	regionOnly.SourceRegion = aws.String("us-west-2")
	sameRegion := s("g", "gone-db", "manual")
	sameRegion.SourceRegion = aws.String("eu-west-1") // this region: not a copy
	return &rds.DescribeDBSnapshotsOutput{DBSnapshots: []rdstypes.DBSnapshot{s("a", "live-db", "manual"), s("b", "gone-db", "manual"), s("c", "gone-db", "automated"), renamed, copied, regionOnly, sameRegion}}, nil
}

func (fakeRDSSnaps) DescribeDBClusterSnapshots(context.Context, *rds.DescribeDBClusterSnapshotsInput, ...func(*rds.Options)) (*rds.DescribeDBClusterSnapshotsOutput, error) {
	return &rds.DescribeDBClusterSnapshotsOutput{DBClusterSnapshots: []rdstypes.DBClusterSnapshot{
		{DBClusterSnapshotIdentifier: aws.String("cs-1"), DBClusterIdentifier: aws.String("gone-cluster"), SnapshotType: aws.String("manual"), Status: aws.String("available"), AllocatedStorage: aws.Int32(100)},
		{DBClusterSnapshotIdentifier: aws.String("cs-copy"), DBClusterIdentifier: aws.String("far-cluster"), SnapshotType: aws.String("manual"), Status: aws.String("available"),
			AllocatedStorage: aws.Int32(100), SourceDBClusterSnapshotArn: aws.String("arn:aws:rds:us-east-1:1:cluster-snapshot:x")},
	}}, nil
}

func TestLoadDBSnapshots(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	if _, err := loadDBSnapshots(context.Background(), r, fakeRDSSnaps{}); err != nil {
		t.Fatal(err)
	}
	if len(r.DBSnapshots) != 8 {
		t.Fatalf("want 6 manual DB snapshots and 2 cluster snapshots, got %+v", r.DBSnapshots)
	}
	// aurora-serverless-pause reads the clusters on the way.
	if len(r.DBClusters) != 2 || r.DBClusters[0].ServerlessV2 {
		t.Fatalf("clusters: %+v", r.DBClusters)
	}
	if c := r.DBClusters[1]; !c.ServerlessV2 || c.MinACU != 0 || c.MaxACU != 8 || c.PauseAfter != 600 || c.Global != "g-1" || !c.DataAPI || !c.ActivityStream ||
		c.StorageType != "aurora-iopt1" || len(c.Members) != 2 || !c.Members[0].Writer || c.Members[0].Tier != 1 || c.Members[1].Writer || c.Members[1].Tier != 2 {
		t.Errorf("a Serverless v2 cluster: %+v", c)
	}
	a, b, renamed, copied, cs := r.DBSnapshots[0], r.DBSnapshots[1], r.DBSnapshots[2], r.DBSnapshots[3], r.DBSnapshots[6]
	if regionOnly, same, csCopy := r.DBSnapshots[4], r.DBSnapshots[5], r.DBSnapshots[7]; !regionOnly.Copy || regionOnly.CopyFrom != "us-west-2" || same.Copy || !csCopy.Copy || cs.Copy {
		t.Fatalf("copies: by SourceRegion alone, by SourceDBClusterSnapshotArn; a same-region SourceRegion is not one: %+v", r.DBSnapshots)
	}
	if !a.SourceExists || b.SourceExists || !b.FreeAllowance || !cs.Cluster || cs.SourceExists || cs.FreeAllowance {
		t.Fatalf("source/allowance wrong (cluster snapshots of deleted clusters are always billed): %+v", r.DBSnapshots)
	}
	if !renamed.SourceExists || !copied.Copy || copied.CopyFrom != "us-east-1" || b.Copy {
		t.Fatalf("a renamed database is live by its resource ID; a copy is marked: %+v %+v", renamed, copied)
	}
	// An Aurora member instance gives no allowance to plain RDS snapshots.
	r = &Region{Name: "eu-west-1"}
	if _, err := loadDBSnapshots(context.Background(), r, fakeRDSSnaps{noInstances: true}); err != nil || r.DBSnapshots[1].FreeAllowance || r.DBSnapshots[2].SourceExists {
		t.Fatalf("cluster members must not count as RDS instances: %v %+v", err, r.DBSnapshots)
	}
}

type fakeHosts struct{}

func (fakeHosts) DescribeHosts(context.Context, *ec2.DescribeHostsInput, ...func(*ec2.Options)) (*ec2.DescribeHostsOutput, error) {
	return &ec2.DescribeHostsOutput{Hosts: []ec2types.Host{
		{HostId: aws.String("h-1"), State: ec2types.AllocationStateAvailable, HostProperties: &ec2types.HostProperties{InstanceType: aws.String("m5.large")}},
		{HostId: aws.String("h-2"), State: ec2types.AllocationStateReleased},
		{HostId: aws.String("h-3"), State: ec2types.AllocationStateAvailable, HostProperties: &ec2types.HostProperties{InstanceFamily: aws.String("c5")},
			Instances: []ec2types.HostInstance{{InstanceId: aws.String("i-1")}}},
		{HostId: aws.String("h-shared"), OwnerId: aws.String("222222222222"), State: ec2types.AllocationStateAvailable},
	}}, nil
}

func TestLoadHosts(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, Hosts: fakeHosts{}})
	r.Hosts = r.Hosts[:2] // without an account, the shared host can't be told apart
	if len(r.Hosts) != 2 || r.Hosts[0].Family != "m5" || r.Hosts[0].Type != "m5.large" || r.Hosts[1].Instances != 1 {
		t.Fatalf("got %+v, want h-1 (family from its type) and h-3 with one instance; released hosts dropped", r.Hosts)
	}
}

type fakeEKSFree struct{ failNodegroups, failDescribe, failFargate, failCapabilities string }

func (fakeEKSFree) ListClusters(context.Context, *eks.ListClustersInput, ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	return &eks.ListClustersOutput{Clusters: []string{"empty", "auto", "hybrid", "broken"}}, nil
}

func (f fakeEKSFree) ListCapabilities(_ context.Context, in *eks.ListCapabilitiesInput, _ ...func(*eks.Options)) (*eks.ListCapabilitiesOutput, error) {
	if aws.ToString(in.ClusterName) == f.failCapabilities {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
	}
	if aws.ToString(in.ClusterName) == "hybrid" {
		return &eks.ListCapabilitiesOutput{Capabilities: []ekstypes.CapabilitySummary{{}}}, nil
	}
	return &eks.ListCapabilitiesOutput{}, nil
}

func (f fakeEKSFree) DescribeCluster(_ context.Context, in *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	if aws.ToString(in.Name) == f.failDescribe {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
	}
	c := &ekstypes.Cluster{Name: in.Name, Status: ekstypes.ClusterStatusActive, Version: aws.String("1.33"),
		ResourcesVpcConfig: &ekstypes.VpcConfigResponse{VpcId: aws.String("vpc-" + aws.ToString(in.Name))}}
	switch aws.ToString(in.Name) {
	case "empty":
		c.DeletionProtection = aws.Bool(true)
		c.ControlPlaneScalingConfig = &ekstypes.ControlPlaneScalingConfig{Tier: ekstypes.ProvisionedControlPlaneTierTierXl}
	case "auto":
		c.ComputeConfig = &ekstypes.ComputeConfigResponse{Enabled: aws.Bool(true)}
	case "hybrid":
		c.RemoteNetworkConfig = &ekstypes.RemoteNetworkConfigResponse{}
	}
	return &eks.DescribeClusterOutput{Cluster: c}, nil
}

func (f fakeEKSFree) ListNodegroups(_ context.Context, in *eks.ListNodegroupsInput, _ ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error) {
	if aws.ToString(in.ClusterName) == f.failNodegroups {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
	}
	if aws.ToString(in.ClusterName) == "hybrid" {
		return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-1"}}, nil
	}
	return &eks.ListNodegroupsOutput{}, nil
}

func (f fakeEKSFree) ListFargateProfiles(_ context.Context, in *eks.ListFargateProfilesInput, _ ...func(*eks.Options)) (*eks.ListFargateProfilesOutput, error) {
	if aws.ToString(in.ClusterName) == f.failFargate {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
	}
	return &eks.ListFargateProfilesOutput{}, nil
}

func TestLoadEKS(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	if _, err := loadEKS(context.Background(), r, fakeEKSFree{failNodegroups: "broken"}); err != nil {
		t.Fatal(err)
	}
	by := map[string]EKSInfo{}
	for _, c := range r.EKSClusters {
		by[c.Name] = c
	}
	if len(by) != 3 || !by["auto"].AutoMode || !by["hybrid"].Hybrid || by["hybrid"].NodeGroups != 1 || by["hybrid"].Capabilities != 1 || by["empty"].NodeGroups != 0 || by["empty"].Version != "1.33" {
		t.Fatalf("got %+v", r.EKSClusters)
	}
	if e := by["empty"]; e.VpcID != "vpc-empty" || e.Tier != "tier-xl" || !e.DeletionProtection || by["auto"].Tier != "standard" {
		t.Fatalf("VPC, tier and deletion protection: %+v", by)
	}
	if _, ok := by["broken"]; ok || len(r.Skipped) != 1 {
		t.Fatalf("a cluster whose node groups can't be listed must be left out, with a skip: %+v %+v", r.EKSClusters, r.Skipped)
	}
	// Any read that fails for a cluster leaves that cluster out: it would look empty.
	for _, f := range []fakeEKSFree{{failDescribe: "empty"}, {failFargate: "empty"}, {failCapabilities: "empty"}} {
		r := &Region{Name: "eu-west-1"}
		if _, err := loadEKS(context.Background(), r, f); err != nil {
			t.Fatal(err)
		}
		for _, c := range r.EKSClusters {
			if c.Name == "empty" {
				t.Errorf("%+v: the cluster must be left out", f)
			}
		}
		if len(r.Skipped) != 1 {
			t.Errorf("%+v: want one skip, got %v", f, r.Skipped)
		}
	}
}

type fakeRoutes struct{ fail bool }

func (f fakeRoutes) DescribeRouteTables(_ context.Context, in *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	if f.fail {
		return nil, &smithy.GenericAPIError{Code: "RequestLimitExceeded"}
	}
	if in.NextToken == nil { // two pages: targets on page 2 still count
		return &ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{{Routes: []ec2types.Route{
			{GatewayId: aws.String("igw-1")}, {NetworkInterfaceId: aws.String("eni-fw")}}}}, NextToken: aws.String("p2")}, nil
	}
	return &ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{{VpcId: aws.String("vpc-1"), RouteTableId: aws.String("rtb-2"), Routes: []ec2types.Route{
		{NatGatewayId: aws.String("nat-used")}, {IpAddress: aws.String("10.0.9.9")}, // a VPC Route Server next hop
		{GatewayId: aws.String("vpce-gwlb1"), DestinationCidrBlock: aws.String("0.0.0.0/0")},             // a Gateway Load Balancer endpoint
		{GatewayId: aws.String("vpce-s3gw"), DestinationPrefixListId: aws.String("pl-63a5400a")}}}}}, nil // a gateway endpoint
}

func TestLoadRoutes(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, Routes: fakeRoutes{}})
	var got []string
	for k := range r.NATRouted {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "eni:eni-fw ip:vpc-1/10.0.9.9 nat:nat-used" {
		t.Errorf("route targets from every page, by NAT ID, network interface and next-hop address: %v", got)
	}
	if g := r.EndpointRoutes["vpce-gwlb1"]; len(g) != 1 || g[0] != "rtb-2 0.0.0.0/0" || len(r.EndpointRoutes["vpce-s3gw"]) != 1 {
		t.Errorf("routes to VPC endpoints, by route table and destination: %v", r.EndpointRoutes)
	}
	r = Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, Routes: fakeRoutes{fail: true}})
	if r.NATRouted != nil || r.EndpointRoutes != nil {
		t.Errorf("a failed route table read must leave the routed set unknown: %v %v", r.NATRouted, r.EndpointRoutes)
	}
}

type fakeClassic struct{}

func (fakeClassic) DescribeLoadBalancers(_ context.Context, in *elb.DescribeLoadBalancersInput, _ ...func(*elb.Options)) (*elb.DescribeLoadBalancersOutput, error) {
	lb := func(name string, n int) elbtypes.LoadBalancerDescription {
		d := elbtypes.LoadBalancerDescription{LoadBalancerName: aws.String(name), DNSName: aws.String(name + ".elb"), VPCId: aws.String("vpc-1")}
		for i := 0; i < n; i++ {
			d.Instances = append(d.Instances, elbtypes.Instance{InstanceId: aws.String(fmt.Sprintf("i-%s-%d", name, i))})
		}
		return d
	}
	if in.Marker == nil { // two pages
		return &elb.DescribeLoadBalancersOutput{LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{lb("empty", 0)}, NextMarker: aws.String("p2")}, nil
	}
	return &elb.DescribeLoadBalancersOutput{LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{lb("half", 3), lb("unread", 2)}}, nil
}

func (fakeClassic) DescribeInstanceHealth(_ context.Context, in *elb.DescribeInstanceHealthInput, _ ...func(*elb.Options)) (*elb.DescribeInstanceHealthOutput, error) {
	if aws.ToString(in.LoadBalancerName) == "unread" {
		return nil, &smithy.GenericAPIError{Code: "Throttling"}
	}
	st := func(s string) elbtypes.InstanceState { return elbtypes.InstanceState{State: aws.String(s)} }
	return &elb.DescribeInstanceHealthOutput{InstanceStates: []elbtypes.InstanceState{st("InService"), st("OutOfService"), st("InService")}}, nil
}

func (f fakeClassic) DescribeTags(_ context.Context, in *elb.DescribeTagsInput, _ ...func(*elb.Options)) (*elb.DescribeTagsOutput, error) {
	var out []elbtypes.TagDescription
	for _, n := range in.LoadBalancerNames {
		d := elbtypes.TagDescription{LoadBalancerName: aws.String(n)}
		if n == "empty" { // made by Kubernetes' in-tree cloud provider
			d.Tags = []elbtypes.Tag{{Key: aws.String("kubernetes.io/cluster/prod"), Value: aws.String("owned")}, {Key: aws.String("kubernetes.io/service-name"), Value: aws.String("web/front")}}
		}
		out = append(out, d)
	}
	return &elb.DescribeTagsOutput{TagDescriptions: out}, nil
}

func TestLoadClassicLBs(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	var skips []string
	loadClassicLBs(context.Background(), r, fakeClassic{}, func(what string, _ error) { skips = append(skips, what) })
	if strings.Join(skips, " ") != "elasticloadbalancing:DescribeInstanceHealth" {
		t.Fatalf("a failed health read is reported: %v", skips)
	}
	got := map[string]ClassicLB{}
	for _, lb := range r.ClassicLBs {
		got[lb.Name] = lb
	}
	if len(got) != 3 || got["empty"].Instances != 0 || got["half"].InService != 2 || got["half"].Instances != 3 || got["unread"].InService != -1 || got["empty"].DNS != "empty.elb" {
		t.Fatalf("every page, in-service counts, unknown health as -1: %+v", r.ClassicLBs)
	}
	if got["empty"].Kubernetes != "prod" || got["half"].Kubernetes != "" {
		t.Errorf("tags read for the ones with nothing in service: %+v", r.ClassicLBs)
	}
}

type fakeLBTags struct {
	calls [][]string
	fail  bool
}

func (f *fakeLBTags) DescribeTags(_ context.Context, in *elbv2.DescribeTagsInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeTagsOutput, error) {
	f.calls = append(f.calls, in.ResourceArns)
	if f.fail {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	var out []elbv2types.TagDescription
	for _, a := range in.ResourceArns {
		d := elbv2types.TagDescription{ResourceArn: aws.String(a)}
		switch a {
		case "arn:lb/controller":
			d.Tags = []elbv2types.Tag{{Key: aws.String("elbv2.k8s.aws/cluster"), Value: aws.String("prod")}, {Key: aws.String("ingress.k8s.aws/stack"), Value: aws.String("web")}}
		case "arn:lb/stack-only":
			d.Tags = []elbv2types.Tag{{Key: aws.String("service.k8s.aws/stack"), Value: aws.String("ns/svc")}}
		}
		out = append(out, d)
	}
	return &elbv2.DescribeTagsOutput{TagDescriptions: out}, nil
}

func TestLoadLBTags(t *testing.T) {
	lbs := []LoadBalancer{{ARN: "arn:lb/busy", Targets: 3}, {ARN: "arn:lb/controller"}, {ARN: "arn:lb/stack-only"}, {ARN: "arn:lb/plain"}}
	for i := 0; i < 20; i++ {
		lbs = append(lbs, LoadBalancer{ARN: fmt.Sprintf("arn:lb/extra-%d", i)})
	}
	c := &fakeLBTags{}
	loadLBTags(context.Background(), lbs, c, func(string, error) { t.Error("no skip expected") })
	if len(c.calls) != 2 || len(c.calls[0]) != 20 || len(c.calls[1]) != 3 {
		t.Errorf("only the ones with no targets, 20 at a time: %v", c.calls)
	}
	if lbs[1].Kubernetes != "prod" || lbs[2].Kubernetes != "?" || lbs[3].Kubernetes != "" || lbs[0].Kubernetes != "" {
		t.Errorf("Kubernetes: %+v", lbs[:4])
	}
	lbs = []LoadBalancer{{ARN: "arn:lb/a"}}
	var skips []string
	loadLBTags(context.Background(), lbs, &fakeLBTags{fail: true}, func(what string, _ error) { skips = append(skips, what) })
	if !lbs[0].TagsUnknown || len(skips) != 1 || skips[0] != "elasticloadbalancing:DescribeTags" {
		t.Errorf("a failed tag read is unknown and a skip: %+v %v", lbs, skips)
	}
}

type fakeLBRules struct {
	listenerCalls []string
	failRules     bool
}

func (f *fakeLBRules) DescribeListeners(_ context.Context, in *elbv2.DescribeListenersInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeListenersOutput, error) {
	arn := aws.ToString(in.LoadBalancerArn)
	f.listenerCalls = append(f.listenerCalls, arn)
	fwd := []elbv2types.Action{{Type: elbv2types.ActionTypeEnumForward}}
	switch arn {
	case "arn:lb/redirect": // the default action redirects (HTTP to HTTPS)
		return &elbv2.DescribeListenersOutput{Listeners: []elbv2types.Listener{{ListenerArn: aws.String("l-redirect"),
			DefaultActions: []elbv2types.Action{{Type: elbv2types.ActionTypeEnumRedirect}}}}}, nil
	case "arn:lb/rule-fixed": // forwards by default, a rule answers; an OIDC login with its secret
		return &elbv2.DescribeListenersOutput{Listeners: []elbv2types.Listener{{ListenerArn: aws.String("l-fixed"),
			DefaultActions: []elbv2types.Action{{Type: elbv2types.ActionTypeEnumAuthenticateOidc,
				AuthenticateOidcConfig: &elbv2types.AuthenticateOidcActionConfig{ClientSecret: aws.String("planted-oidc-client-secret")}}, fwd[0]}}}}, nil
	}
	return &elbv2.DescribeListenersOutput{Listeners: []elbv2types.Listener{{ListenerArn: aws.String("l-" + arn), DefaultActions: fwd}}}, nil
}

func (f *fakeLBRules) DescribeRules(_ context.Context, in *elbv2.DescribeRulesInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeRulesOutput, error) {
	if f.failRules {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	if aws.ToString(in.ListenerArn) == "l-fixed" {
		return &elbv2.DescribeRulesOutput{Rules: []elbv2types.Rule{{Actions: []elbv2types.Action{{Type: elbv2types.ActionTypeEnumFixedResponse,
			FixedResponseConfig: &elbv2types.FixedResponseActionConfig{StatusCode: aws.String("200"), MessageBody: aws.String("planted-fixed-response-body")}}}}}}, nil
	}
	return &elbv2.DescribeRulesOutput{Rules: []elbv2types.Rule{{Actions: []elbv2types.Action{{Type: elbv2types.ActionTypeEnumForward}}}}}, nil
}

func TestLoadLBListeners(t *testing.T) {
	lbs := []LoadBalancer{{ARN: "arn:lb/busy", Type: "application", Targets: 2}, {ARN: "arn:lb/nlb", Type: "network"},
		{ARN: "arn:lb/redirect", Type: "application"}, {ARN: "arn:lb/rule-fixed", Type: "application"}, {ARN: "arn:lb/idle", Type: "application", TargetGroups: 1}}
	c := &fakeLBRules{}
	loadLBListeners(context.Background(), lbs, c, func(what string, _ error) { t.Errorf("no skip expected: %s", what) })
	if strings.Join(c.listenerCalls, ",") != "arn:lb/redirect,arn:lb/rule-fixed,arn:lb/idle" {
		t.Errorf("only ALBs with no targets are read: %v", c.listenerCalls)
	}
	if lbs[2].Responds != 1 || lbs[3].Responds != 1 || lbs[4].Responds != 0 || lbs[0].Responds != 0 || lbs[1].Responds != 0 {
		t.Errorf("listeners that answer: %+v", lbs)
	}
	if b, _ := json.Marshal(lbs); strings.Contains(string(b), "planted") {
		t.Errorf("only action types are kept: %s", b)
	}
	// A failed read leaves that one and the rest unknown, with one skip line.
	lbs = []LoadBalancer{{ARN: "arn:lb/idle", Type: "application"}, {ARN: "arn:lb/idle-2", Type: "application"}}
	var skips []string
	loadLBListeners(context.Background(), lbs, &fakeLBRules{failRules: true}, func(what string, _ error) { skips = append(skips, what) })
	if !lbs[0].ListenersUnknown || !lbs[1].ListenersUnknown || len(skips) != 1 || skips[0] != "elasticloadbalancing:DescribeRules" {
		t.Errorf("a failed read is unknown, one skip: %+v %v", lbs, skips)
	}
}

type fakeRDSFull struct{ fakeRDSSnaps }

func (fakeRDSFull) DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	return &rds.DescribeDBInstancesOutput{DBInstances: []rdstypes.DBInstance{
		{DBInstanceIdentifier: aws.String("primary"), DBInstanceArn: aws.String("arn:aws:rds:eu-west-1:1:db:primary"), DbiResourceId: aws.String("db-P"),
			DBInstanceClass: aws.String("db.m4.large"), Engine: aws.String("mysql"), EngineVersion: aws.String("8.0.36"), DBInstanceStatus: aws.String("available"),
			LicenseModel: aws.String("general-public-license"), MultiAZ: aws.Bool(true), DeletionProtection: aws.Bool(true), StorageType: aws.String("gp3"),
			AllocatedStorage: aws.Int32(100), ReadReplicaDBInstanceIdentifiers: []string{"r1"}, ReadReplicaDBClusterIdentifiers: []string{"c1"}},
		{DBInstanceIdentifier: aws.String("r1"), ReadReplicaSourceDBInstanceIdentifier: aws.String("primary")},
		{DBInstanceIdentifier: aws.String("from-cluster"), ReadReplicaSourceDBClusterIdentifier: aws.String("aurora-src")},
	}}, nil
}

func TestLoadDBInstances(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	if _, err := loadDBSnapshots(context.Background(), r, fakeRDSFull{}); err != nil {
		t.Fatal(err)
	}
	if len(r.DBInstances) != 3 {
		t.Fatalf("instances: %+v", r.DBInstances)
	}
	p := r.DBInstances[0]
	if p.ARN == "" || p.ResourceID != "db-P" || p.Class != "db.m4.large" || p.Version != "8.0.36" || !p.MultiAZ || !p.DeletionProtection || p.GiB != 100 || p.Replicas != 2 || p.License != "general-public-license" {
		t.Errorf("primary: %+v", p)
	}
	if r1, r2 := r.DBInstances[1], r.DBInstances[2]; r1.ReplicaOf != "primary" || r1.ReplicaOfCluster || r2.ReplicaOf != "aurora-src" || !r2.ReplicaOfCluster {
		t.Errorf("a replica of an instance or of a cluster: %+v %+v", r1, r2)
	}
}

type fakeS3 struct{ lockCalls []string }

func (f *fakeS3) ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	var out []s3types.Bucket
	for _, n := range []string{"b-plain", "b-expires", "b-keep", "b-locked", "b-mfa", "b-verr", "b-lockerr"} {
		out = append(out, s3types.Bucket{Name: aws.String(n), BucketRegion: aws.String("eu-west-1")})
	}
	return &s3.ListBucketsOutput{Buckets: out}, nil
}

func (f *fakeS3) GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return &s3.GetBucketLocationOutput{}, nil
}

func (f *fakeS3) GetBucketLifecycleConfiguration(_ context.Context, in *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	if aws.ToString(in.Bucket) == "b-expires" {
		return &s3.GetBucketLifecycleConfigurationOutput{Rules: []s3types.LifecycleRule{{Status: s3types.ExpirationStatusEnabled,
			NoncurrentVersionExpiration: &s3types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(30)}}}}, nil
	}
	return nil, &smithy.GenericAPIError{Code: "NoSuchLifecycleConfiguration"}
}

func (f *fakeS3) GetBucketVersioning(_ context.Context, in *s3.GetBucketVersioningInput, _ ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	switch aws.ToString(in.Bucket) {
	case "b-plain":
		return &s3.GetBucketVersioningOutput{}, nil
	case "b-verr":
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	case "b-mfa":
		return &s3.GetBucketVersioningOutput{Status: s3types.BucketVersioningStatusEnabled, MFADelete: s3types.MFADeleteStatusEnabled}, nil
	}
	return &s3.GetBucketVersioningOutput{Status: s3types.BucketVersioningStatusEnabled, MFADelete: s3types.MFADeleteStatusDisabled}, nil
}

func (f *fakeS3) GetObjectLockConfiguration(_ context.Context, in *s3.GetObjectLockConfigurationInput, _ ...func(*s3.Options)) (*s3.GetObjectLockConfigurationOutput, error) {
	f.lockCalls = append(f.lockCalls, aws.ToString(in.Bucket))
	switch aws.ToString(in.Bucket) {
	case "b-locked":
		return &s3.GetObjectLockConfigurationOutput{ObjectLockConfiguration: &s3types.ObjectLockConfiguration{ObjectLockEnabled: s3types.ObjectLockEnabledEnabled}}, nil
	case "b-lockerr":
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	return nil, &smithy.GenericAPIError{Code: "ObjectLockConfigurationNotFoundError"}
}

func TestLoadBucketVersioning(t *testing.T) {
	c := &fakeS3{}
	all, skips := LoadBuckets(context.Background(), c, func(string) S3API { return c })
	got := map[string]Bucket{}
	for _, b := range all {
		got[b.Name] = b
	}
	if got["b-plain"].Versioning != "" || got["b-keep"].Versioning != "Enabled" || got["b-keep"].ExpiresNoncurrent || got["b-keep"].ObjectLock != "" ||
		!got["b-expires"].ExpiresNoncurrent || got["b-locked"].ObjectLock != "Enabled" || !got["b-mfa"].MFADelete ||
		got["b-verr"].Versioning != "?" || got["b-lockerr"].ObjectLock != "?" {
		t.Errorf("buckets: %+v", got)
	}
	if strings.Join(c.lockCalls, " ") != "b-keep b-locked b-mfa b-lockerr" {
		t.Errorf("Object Lock is read only for versioned buckets that keep every version: %v", c.lockCalls)
	}
	var whats []string
	for _, s := range skips {
		whats = append(whats, s.What)
	}
	if strings.Join(whats, " ") != "s3:GetBucketVersioning s3:GetBucketObjectLockConfiguration" {
		t.Errorf("skips: %v", whats)
	}
}
