package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeFleets struct{ fail bool }

func (f fakeFleets) DescribeFleets(_ context.Context, in *ec2.DescribeFleetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeFleetsOutput, error) {
	if f.fail {
		return nil, errors.New("UnauthorizedOperation")
	}
	if in.NextToken == nil {
		return &ec2.DescribeFleetsOutput{NextToken: aws.String("p2"), Fleets: []ec2types.FleetData{
			{FleetId: aws.String("fleet-maintain"), FleetState: ec2types.FleetStateCodeActive, Type: ec2types.FleetTypeMaintain},
		}}, nil
	}
	return &ec2.DescribeFleetsOutput{Fleets: []ec2types.FleetData{
		{FleetId: aws.String("fleet-instant"), FleetState: ec2types.FleetStateCodeActive, Type: ec2types.FleetTypeInstant},
		{FleetId: aws.String("fleet-gone"), FleetState: ec2types.FleetStateCodeDeleted, Type: ec2types.FleetTypeRequest},
	}}, nil
}

type fakeSpotFleets struct{ fail bool }

func (f fakeSpotFleets) DescribeSpotFleetRequests(_ context.Context, in *ec2.DescribeSpotFleetRequestsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSpotFleetRequestsOutput, error) {
	if f.fail {
		return nil, errors.New("UnauthorizedOperation")
	}
	secret := "hunter2-userdata"
	cfg := &ec2types.SpotFleetRequestConfigData{
		IamFleetRole:         aws.String("arn:aws:iam::111111111111:role/spot"),
		TargetCapacity:       aws.Int32(0),
		LaunchSpecifications: []ec2types.SpotFleetLaunchSpecification{{UserData: aws.String(secret)}},
	}
	if in.NextToken == nil {
		return &ec2.DescribeSpotFleetRequestsOutput{NextToken: aws.String("p2"), SpotFleetRequestConfigs: []ec2types.SpotFleetRequestConfig{
			{SpotFleetRequestId: aws.String("sfr-active"), SpotFleetRequestState: ec2types.BatchStateActive, SpotFleetRequestConfig: cfg},
		}}, nil
	}
	return &ec2.DescribeSpotFleetRequestsOutput{SpotFleetRequestConfigs: []ec2types.SpotFleetRequestConfig{
		{SpotFleetRequestId: aws.String("sfr-modifying"), SpotFleetRequestState: ec2types.BatchStateModifying},
		{SpotFleetRequestId: aws.String("sfr-cancelled"), SpotFleetRequestState: ec2types.BatchStateCancelled},
	}}, nil
}

type fakeBatchCE struct{ fail bool }

func (f fakeBatchCE) DescribeComputeEnvironments(_ context.Context, in *batch.DescribeComputeEnvironmentsInput, _ ...func(*batch.Options)) (*batch.DescribeComputeEnvironmentsOutput, error) {
	if f.fail {
		return nil, errors.New("AccessDeniedException")
	}
	ce := func(name, typ, state, status, resource string) batchtypes.ComputeEnvironmentDetail {
		d := batchtypes.ComputeEnvironmentDetail{
			ComputeEnvironmentName: aws.String(name),
			ComputeEnvironmentArn:  aws.String("arn:aws:batch:eu-west-1:111111111111:compute-environment/" + name),
			Type:                   batchtypes.CEType(typ),
			State:                  batchtypes.CEState(state),
			Status:                 batchtypes.CEStatus(status),
		}
		if resource != "" {
			d.ComputeResources = &batchtypes.ComputeResource{Type: batchtypes.CRType(resource)}
		}
		return d
	}
	if in.NextToken == nil {
		return &batch.DescribeComputeEnvironmentsOutput{NextToken: aws.String("p2"), ComputeEnvironments: []batchtypes.ComputeEnvironmentDetail{
			ce("ec2-on", "MANAGED", "ENABLED", "VALID", "EC2"),
		}}, nil
	}
	return &batch.DescribeComputeEnvironmentsOutput{ComputeEnvironments: []batchtypes.ComputeEnvironmentDetail{
		ce("spot-off", "MANAGED", "DISABLED", "VALID", "SPOT"),
		ce("fargate", "MANAGED", "ENABLED", "VALID", "FARGATE"),
		ce("deleted", "MANAGED", "ENABLED", "DELETED", "EC2"),
		ce("unmanaged", "UNMANAGED", "ENABLED", "VALID", ""),
	}}, nil
}

func TestLoadLaunchers(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{
		EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{},
		Fleets: fakeFleets{}, SpotFleets: fakeSpotFleets{}, Batch: fakeBatchCE{},
	})
	if len(r.EC2Fleets) != 3 || r.EC2Fleets[0].ID != "fleet-maintain" || r.EC2Fleets[1].Type != "instant" {
		t.Fatalf("fleets: %+v", r.EC2Fleets)
	}
	if len(r.SpotFleets) != 3 || r.SpotFleets[0].ID != "sfr-active" || r.SpotFleets[0].State != "active" {
		t.Fatalf("spot fleets: %+v", r.SpotFleets)
	}
	b, _ := json.Marshal(r.SpotFleets)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "LaunchSpec") || strings.Contains(string(b), "UserData") {
		t.Fatalf("Spot Fleet LaunchSpecifications/UserData must be dropped: %s", b)
	}
	if len(r.BatchCEs) != 5 || r.BatchCEs[0].Name != "ec2-on" || r.BatchCEs[0].ResourceType != "EC2" || r.BatchCEs[1].State != "DISABLED" {
		t.Fatalf("batch: %+v", r.BatchCEs)
	}
}

func TestLoadLaunchersFailures(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{
		EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{},
		Fleets: fakeFleets{fail: true}, SpotFleets: fakeSpotFleets{fail: true}, Batch: fakeBatchCE{fail: true},
	})
	sk := skipsOf(r)
	if r.EC2Fleets != nil || r.SpotFleets != nil || r.BatchCEs != nil {
		t.Errorf("failed reads leave nil: fleets=%v spot=%v batch=%v", r.EC2Fleets, r.SpotFleets, r.BatchCEs)
	}
	for _, want := range []string{"ec2:DescribeFleets", "ec2:DescribeSpotFleetRequests", "batch:DescribeComputeEnvironments"} {
		if !strings.Contains(sk, want) {
			t.Errorf("missing skip %s in %q", want, sk)
		}
	}
}

func TestLoadLaunchersNilClients(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}})
	if r.EC2Fleets != nil || r.SpotFleets != nil || r.BatchCEs != nil {
		t.Errorf("nil clients: not read: fleets=%v spot=%v batch=%v", r.EC2Fleets, r.SpotFleets, r.BatchCEs)
	}
}
