package inventory

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ComputeOptimizerSavingsSource is a plain note when some recommendations lack a
// usable USD estimate. It is not a Meta Source: only those resources go in
// Unjudged so other Compute Optimizer fixes in the region can still book.
const ComputeOptimizerSavingsSource = "compute-optimizer:SavingsOpportunity"

// ComputeOptimizerAPI is the AWS Compute Optimizer calls (us-east-1 endpoint).
// Only GetLambdaFunctionRecommendations has a generated APIClient interface in
// this SDK version; the rest are named here to match *computeoptimizer.Client.
type ComputeOptimizerAPI interface {
	GetEnrollmentStatus(context.Context, *computeoptimizer.GetEnrollmentStatusInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetEnrollmentStatusOutput, error)
	GetEC2InstanceRecommendations(context.Context, *computeoptimizer.GetEC2InstanceRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetEC2InstanceRecommendationsOutput, error)
	GetAutoScalingGroupRecommendations(context.Context, *computeoptimizer.GetAutoScalingGroupRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetAutoScalingGroupRecommendationsOutput, error)
	GetEBSVolumeRecommendations(context.Context, *computeoptimizer.GetEBSVolumeRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetEBSVolumeRecommendationsOutput, error)
	GetRDSDatabaseRecommendations(context.Context, *computeoptimizer.GetRDSDatabaseRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetRDSDatabaseRecommendationsOutput, error)
	GetECSServiceRecommendations(context.Context, *computeoptimizer.GetECSServiceRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetECSServiceRecommendationsOutput, error)
	GetIdleRecommendations(context.Context, *computeoptimizer.GetIdleRecommendationsInput, ...func(*computeoptimizer.Options)) (*computeoptimizer.GetIdleRecommendationsOutput, error)
	computeoptimizer.GetLambdaFunctionRecommendationsAPIClient
}

// ComputeOptimizerRec is one over-provisioned rightsizing option or idle
// recommendation from Compute Optimizer with USD savings above 0. Savings are
// AWS's own estimate. Kind uses Cost Optimization Hub resource type names so
// DropCovered can match our priced checks.
type ComputeOptimizerRec struct {
	Kind, Region, ResourceID, ResourceARN string // Kind: Ec2Instance, Ec2AutoScalingGroup, EbsVolume, LambdaFunction, RdsDbInstance, EcsService, NatGateway, …
	Current, Recommended                  string // type/size summaries (rightsizing); idle uses FindingDescription as Current
	Finding, FindingReasons               string
	LookbackDays                          int
	MonthlySavings                        float64
	AfterDiscounts                        bool
	PerformanceRisk                       float64 // option risk 0–4; >1 is above Low (idle has none → 0)
	AccountID                             string
}

// ComputeOptimizer is what Compute Optimizer said for the account (or the
// scanned regions of it).
type ComputeOptimizer struct {
	Enrolled         bool
	Recs             []ComputeOptimizerRec
	CoveredInstances []ComputeOptimizerRec // parked because their Auto Scaling group is counted
	Unjudged         []string              // "|region|compute-optimizer|"+Key for recommendations without usable USD savings
}

// InstanceASGNames maps each instance's aws:autoscaling:groupName tag from
// inventory EC2 data (instance ID → group name).
func InstanceASGNames(instances []ec2types.Instance) map[string]string {
	var out map[string]string
	for _, in := range instances {
		id := aws.ToString(in.InstanceId)
		if id == "" {
			continue
		}
		for _, tg := range in.Tags {
			if aws.ToString(tg.Key) != "aws:autoscaling:groupName" {
				continue
			}
			if name := aws.ToString(tg.Value); name != "" {
				if out == nil {
					out = map[string]string{}
				}
				out[id] = name
			}
			break
		}
	}
	return out
}
