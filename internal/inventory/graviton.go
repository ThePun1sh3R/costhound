package inventory

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/pricing"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// gravitonSkipTags mark an instance something else launches and replaces
// (a fleet, EMR, Beanstalk, Batch): its owner can't pick its type here.
// Auto Scaling groups, EKS node groups and ECS hosts stay: ec2-graviton
// reports a group once, with how to change its launch template.
var gravitonSkipTags = []string{"aws:ec2:fleet-id", "aws:ec2spot:fleet-request-id", "aws:elasticmapreduce:job-flow-id",
	"elasticbeanstalk:environment-name", "aws:batch:compute-environment"}

// LinuxOnDemand is an instance the price table can state a price for:
// running, On-Demand (not Spot, scheduled or a capacity block), x86_64,
// billed as plain Linux/UNIX (PlatformDetails and UsageOperation: RHEL, SUSE
// and Ubuntu Pro are priced differently), on shared tenancy in a standard
// Availability Zone (not a Local or Wavelength Zone or an Outpost), outside
// a Capacity Reservation (it keeps billing the old type), and not a
// Marketplace product (the seller's fee and builds are its own).
func LinuxOnDemand(in ec2types.Instance, region string) bool {
	if in.State == nil || in.State.Name != ec2types.InstanceStateNameRunning || in.InstanceLifecycle != "" || in.LaunchTime == nil {
		return false
	}
	if in.Architecture != ec2types.ArchitectureValuesX8664 || aws.ToString(in.PlatformDetails) != "Linux/UNIX" ||
		aws.ToString(in.UsageOperation) != "RunInstances" {
		return false
	}
	if aws.ToString(in.CapacityReservationId) != "" || len(in.ProductCodes) > 0 || aws.ToString(in.OutpostArn) != "" {
		return false
	}
	if in.Placement == nil || (in.Placement.Tenancy != "" && in.Placement.Tenancy != ec2types.TenancyDefault) {
		return false
	}
	az := aws.ToString(in.Placement.AvailabilityZone)
	return len(az) == len(region)+1 && strings.HasPrefix(az, region)
}

// GravitonCandidate is an instance ec2-graviton can judge: LinuxOnDemand,
// not run by an AWS service (EKS Auto Mode), Karpenter, a fleet, EMR,
// Beanstalk or Batch, and of a family costhound compares with Graviton.
func GravitonCandidate(in ec2types.Instance, region string) bool {
	if !LinuxOnDemand(in, region) || (in.Operator != nil && aws.ToBool(in.Operator.Managed)) {
		return false
	}
	for _, tg := range in.Tags {
		k := aws.ToString(tg.Key)
		if slices.Contains(gravitonSkipTags, k) || strings.HasPrefix(k, "karpenter.sh/") || strings.HasPrefix(k, "karpenter.k8s.aws/") {
			return false
		}
	}
	family, _, _ := strings.Cut(string(in.InstanceType), ".")
	return pricing.EC2GravitonFamily(family)
}

// compareWith lists the types an instance is compared with: its Graviton
// twins (ec2-graviton), or the current x86 types an old one can move to
// (old-instance-type; not an EMR node, whose type EMR picks).
func compareWith(in ec2types.Instance, region string) []string {
	if GravitonCandidate(in, region) {
		return pricing.EC2GravitonTwins(string(in.InstanceType))
	}
	for _, tg := range in.Tags {
		if aws.ToString(tg.Key) == "aws:elasticmapreduce:job-flow-id" {
			return nil
		}
	}
	if LinuxOnDemand(in, region) {
		return pricing.EC2CurrentOptions(string(in.InstanceType))
	}
	return nil
}

// gravitonWanted: the types the region's instances could move to.
func gravitonWanted(r *Region) map[string]bool {
	want := map[string]bool{}
	for _, in := range r.Instances {
		for _, t := range compareWith(in, r.Name) {
			want[t] = true
		}
	}
	return want
}

// loadOfferings reads which Availability Zones offer the types the checks
// ask about (ec2:DescribeInstanceTypeOfferings by zone): an instance with
// EBS volumes can't leave its zone, and newer types are missing from older
// zones. Nothing to ask: nothing read. A failed read leaves r.Offered nil,
// which is unknown: no finding depends on a guess.
func loadOfferings(ctx context.Context, r *Region, c ec2.DescribeInstanceTypeOfferingsAPIClient, skip func(string, error)) {
	want := gravitonWanted(r)
	types := make([]string, 0, len(want))
	for t := range want {
		types = append(types, t)
	}
	sort.Strings(types)
	offered := map[string]map[string]bool{}
	for len(types) > 0 {
		chunk := types[:min(len(types), 100)]
		types = types[len(chunk):]
		p := ec2.NewDescribeInstanceTypeOfferingsPaginator(c, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: ec2types.LocationTypeAvailabilityZone,
			Filters:      []ec2types.Filter{{Name: aws.String("instance-type"), Values: chunk}},
		})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeInstanceTypeOfferingsOutput) {
			for _, of := range o.InstanceTypeOfferings {
				az := aws.ToString(of.Location)
				if offered[az] == nil {
					offered[az] = map[string]bool{}
				}
				offered[az][string(of.InstanceType)] = true
			}
		}); err != nil {
			skip("ec2:DescribeInstanceTypeOfferings", err)
			return
		}
	}
	r.Offered = offered
}
