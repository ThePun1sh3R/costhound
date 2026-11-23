package inventory

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// EC2Fleet is what ec2-detailed-monitoring keeps of an EC2 Fleet.
type EC2Fleet struct {
	ID, State, Type string
}

// SpotFleet is what ec2-detailed-monitoring keeps of a Spot Fleet request.
// DescribeSpotFleetRequests can answer with LaunchSpecifications that hold
// UserData: only the ID and state are kept.
type SpotFleet struct {
	ID, State string
}

// BatchCE is what ec2-detailed-monitoring keeps of a Batch compute environment.
type BatchCE struct {
	Name, ARN, Type, State, Status, ResourceType string
}

// BatchAPI is the Batch call that lists compute environments.
type BatchAPI interface {
	batch.DescribeComputeEnvironmentsAPIClient
}

// loadEC2Fleets reads the region's EC2 Fleets (nil left = not read).
func loadEC2Fleets(ctx context.Context, r *Region, c ec2.DescribeFleetsAPIClient, skip func(string, error)) {
	out := []EC2Fleet{}
	p := ec2.NewDescribeFleetsPaginator(c, &ec2.DescribeFleetsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeFleetsOutput) {
		for _, f := range o.Fleets {
			out = append(out, EC2Fleet{ID: aws.ToString(f.FleetId), State: string(f.FleetState), Type: string(f.Type)})
		}
	}); err != nil {
		skip("ec2:DescribeFleets", err)
		return
	}
	r.EC2Fleets = out
}

// loadSpotFleets reads the region's Spot Fleet requests and keeps only the
// ID and state: LaunchSpecifications can hold UserData.
func loadSpotFleets(ctx context.Context, r *Region, c ec2.DescribeSpotFleetRequestsAPIClient, skip func(string, error)) {
	out := []SpotFleet{}
	p := ec2.NewDescribeSpotFleetRequestsPaginator(c, &ec2.DescribeSpotFleetRequestsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeSpotFleetRequestsOutput) {
		for _, f := range o.SpotFleetRequestConfigs {
			out = append(out, SpotFleet{ID: aws.ToString(f.SpotFleetRequestId), State: string(f.SpotFleetRequestState)})
		}
	}); err != nil {
		skip("ec2:DescribeSpotFleetRequests", err)
		return
	}
	r.SpotFleets = out
}

// loadBatchCEs reads the region's Batch compute environments (nil left = not read).
func loadBatchCEs(ctx context.Context, r *Region, c BatchAPI, skip func(string, error)) {
	out := []BatchCE{}
	p := batch.NewDescribeComputeEnvironmentsPaginator(c, &batch.DescribeComputeEnvironmentsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *batch.DescribeComputeEnvironmentsOutput) {
		for _, e := range o.ComputeEnvironments {
			ce := BatchCE{
				Name: aws.ToString(e.ComputeEnvironmentName), ARN: aws.ToString(e.ComputeEnvironmentArn),
				Type: string(e.Type), State: string(e.State), Status: string(e.Status),
			}
			if e.ComputeResources != nil {
				ce.ResourceType = string(e.ComputeResources.Type)
			}
			out = append(out, ce)
		}
	}); err != nil {
		skip("batch:DescribeComputeEnvironments", err)
		return
	}
	r.BatchCEs = out
}
