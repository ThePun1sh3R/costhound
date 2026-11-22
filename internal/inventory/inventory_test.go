package inventory

import (
	"context"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"
	"strings"
)

type fakeEC2 struct {
	snapIn  *ec2.DescribeSnapshotsInput
	imageIn *ec2.DescribeImagesInput
}

func (f *fakeEC2) DescribeVolumes(_ context.Context, in *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if in.NextToken == nil { // two pages
		return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{{VolumeId: aws.String("vol-1")}}, NextToken: aws.String("p2")}, nil
	}
	return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{{VolumeId: aws.String("vol-2")}}}, nil
}

func (f *fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{
		{Instances: []ec2types.Instance{{InstanceId: aws.String("i-1")}, {InstanceId: aws.String("i-2")}}},
		{Instances: []ec2types.Instance{{InstanceId: aws.String("i-3")}}},
	}}, nil
}

func (f *fakeEC2) DescribeSnapshots(_ context.Context, in *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	f.snapIn = in
	return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{
		{SnapshotId: aws.String("snap-1"), OwnerId: aws.String("111111111111")},
		{SnapshotId: aws.String("snap-foreign"), OwnerId: aws.String("801119661308")}, // API ignored the self filter
	}}, nil
}

func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	f.imageIn = in
	return &ec2.DescribeImagesOutput{Images: []ec2types.Image{
		{ImageId: aws.String("ami-mine"), OwnerId: aws.String("111111111111")},
		{ImageId: aws.String("ami-amazon"), OwnerId: aws.String("801119661308")},
	}}, nil
}

func (f *fakeEC2) DescribeNatGateways(context.Context, *ec2.DescribeNatGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	return &ec2.DescribeNatGatewaysOutput{}, nil
}

func (f *fakeEC2) DescribeVpcEndpoints(context.Context, *ec2.DescribeVpcEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "nope"}
}

func (f *fakeEC2) DescribeAddresses(context.Context, *ec2.DescribeAddressesInput, ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return &ec2.DescribeAddressesOutput{Addresses: []ec2types.Address{{AllocationId: aws.String("eipalloc-1")}}}, nil
}

type deniedLogs struct{}

func (deniedLogs) DescribeLogGroups(context.Context, *cloudwatchlogs.DescribeLogGroupsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not allowed"}
}

type fakeELB struct{}

func (fakeELB) DescribeLoadBalancers(context.Context, *elb.DescribeLoadBalancersInput, ...func(*elb.Options)) (*elb.DescribeLoadBalancersOutput, error) {
	return &elb.DescribeLoadBalancersOutput{LoadBalancers: []elbtypes.LoadBalancer{
		{LoadBalancerArn: aws.String("arn:a"), LoadBalancerName: aws.String("a"), Type: elbtypes.LoadBalancerTypeEnumApplication},
		{LoadBalancerArn: aws.String("arn:b"), LoadBalancerName: aws.String("b"), Type: elbtypes.LoadBalancerTypeEnumNetwork},
	}}, nil
}

func (fakeELB) DescribeTargetGroups(context.Context, *elb.DescribeTargetGroupsInput, ...func(*elb.Options)) (*elb.DescribeTargetGroupsOutput, error) {
	return &elb.DescribeTargetGroupsOutput{TargetGroups: []elbtypes.TargetGroup{
		{TargetGroupArn: aws.String("tg-a1"), LoadBalancerArns: []string{"arn:a"}},
		{TargetGroupArn: aws.String("tg-a2"), LoadBalancerArns: []string{"arn:a"}},
		{TargetGroupArn: aws.String("tg-b"), LoadBalancerArns: []string{"arn:b"}, VpcId: aws.String("vpc-1")},
		{TargetGroupArn: aws.String("tg-orphan")},
	}}, nil
}

func (fakeELB) DescribeTargetHealth(_ context.Context, in *elb.DescribeTargetHealthInput, _ ...func(*elb.Options)) (*elb.DescribeTargetHealthOutput, error) {
	if aws.ToString(in.TargetGroupArn) == "tg-b" { // an instance target, an IP target in the VPC, and one outside it
		return &elb.DescribeTargetHealthOutput{TargetHealthDescriptions: []elbtypes.TargetHealthDescription{
			{Target: &elbtypes.TargetDescription{Id: aws.String("i-web-1")}},
			{Target: &elbtypes.TargetDescription{Id: aws.String("10.0.0.5"), AvailabilityZone: aws.String("eu-west-1a")}},
			{Target: &elbtypes.TargetDescription{Id: aws.String("172.16.0.9"), AvailabilityZone: aws.String("all")}}}}, nil
	}
	return &elb.DescribeTargetHealthOutput{}, nil
}

func TestLoad(t *testing.T) {
	e := &fakeEC2{}
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: e, Logs: deniedLogs{}, ELB: fakeELB{}})

	if len(r.Volumes) != 2 {
		t.Errorf("volumes = %d, want 2 (both pages)", len(r.Volumes))
	}
	if len(r.Instances) != 3 {
		t.Errorf("instances = %d, want 3 (all reservations)", len(r.Instances))
	}
	if len(r.Addresses) != 1 {
		t.Errorf("addresses = %d", len(r.Addresses))
	}
	// Belt and braces: anything owned by another account is dropped.
	if len(r.Snapshots) != 1 || aws.ToString(r.Snapshots[0].SnapshotId) != "snap-1" {
		t.Errorf("snapshots = %+v, want only snap-1", r.Snapshots)
	}
	if len(r.Images) != 1 || aws.ToString(r.Images[0].ImageId) != "ami-mine" {
		t.Errorf("images = %+v, want only ami-mine", r.Images)
	}

	// The two filters that keep the scan to the account's own resources.
	if e.snapIn == nil || len(e.snapIn.OwnerIds) != 1 || e.snapIn.OwnerIds[0] != "self" {
		t.Errorf("DescribeSnapshots must ask for OwnerIds=[self], got %+v", e.snapIn)
	}
	if e.imageIn == nil || len(e.imageIn.Owners) != 1 || e.imageIn.Owners[0] != "self" ||
		!aws.ToBool(e.imageIn.IncludeDeprecated) || !aws.ToBool(e.imageIn.IncludeDisabled) {
		t.Errorf("DescribeImages must ask for Owners=[self] incl. deprecated+disabled, got %+v", e.imageIn)
	}

	want := map[string]string{"logs:DescribeLogGroups": "AccessDeniedException", "ec2:DescribeVpcEndpoints": "UnauthorizedOperation"}
	if len(r.Skipped) != len(want) {
		t.Fatalf("skipped = %+v, want %v", r.Skipped, want)
	}
	for _, s := range r.Skipped {
		if want[s.What] != s.Error || s.Region != "eu-west-1" {
			t.Errorf("unexpected skip %+v", s)
		}
	}

	if len(r.LoadBalancers) != 2 {
		t.Fatalf("load balancers = %+v", r.LoadBalancers)
	}
	a, b := r.LoadBalancers[0], r.LoadBalancers[1]
	if strings.Join(b.TargetInstances, " ") != "i-web-1" || len(a.TargetInstances) != 0 {
		t.Errorf("registered instances (not IP targets): %v %v", a.TargetInstances, b.TargetInstances)
	}
	if want := []TargetIP{{IP: "10.0.0.5", VPC: "vpc-1"}, {IP: "172.16.0.9"}}; !slices.Equal(b.TargetIPs, want) {
		t.Errorf("IP targets, with the target group's VPC (none outside it): %+v", b.TargetIPs)
	}
	if a.Name != "a" || a.TargetGroups != 2 || a.Targets != 0 || a.Type != "application" {
		t.Errorf("lb a = %+v", a)
	}
	if b.Name != "b" || b.TargetGroups != 1 || b.Targets != 3 {
		t.Errorf("lb b = %+v", b)
	}
}
