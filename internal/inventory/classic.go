package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
)

// ClassicLBAPI is the Classic Load Balancer calls (Elastic Load Balancing
// API version 1, which shares its IAM action names with version 2).
type ClassicLBAPI interface {
	elb.DescribeLoadBalancersAPIClient
	DescribeInstanceHealth(context.Context, *elb.DescribeInstanceHealthInput, ...func(*elb.Options)) (*elb.DescribeInstanceHealthOutput, error)
	DescribeTags(context.Context, *elb.DescribeTagsInput, ...func(*elb.Options)) (*elb.DescribeTagsOutput, error)
}

// ClassicLB is a Classic Load Balancer and the health of its instances.
type ClassicLB struct {
	Name, DNS, VPC string
	Created        *time.Time
	Instances      int // registered
	InstanceIDs    []string
	InService      int    // passing health checks; -1 = unknown (the health read failed)
	Kubernetes     string // the Kubernetes cluster its tags name ("?" if they don't say which), "" if none
	TagsUnknown    bool   // its tags couldn't be read
}

// loadClassicLBs lists the Classic Load Balancers and, for each one with
// registered instances, how many are in service. A load balancer whose
// health can't be read keeps InService -1 (unknown). The ones with none in
// service get their tags read, to see whether Kubernetes made them.
func loadClassicLBs(ctx context.Context, r *Region, c ClassicLBAPI, skip func(string, error)) {
	var lbs []ClassicLB
	p := elb.NewDescribeLoadBalancersPaginator(c, &elb.DescribeLoadBalancersInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *elb.DescribeLoadBalancersOutput) {
		for _, d := range o.LoadBalancerDescriptions {
			lb := ClassicLB{Name: aws.ToString(d.LoadBalancerName), DNS: aws.ToString(d.DNSName),
				VPC: aws.ToString(d.VPCId), Created: d.CreatedTime, Instances: len(d.Instances)}
			for _, in := range d.Instances {
				lb.InstanceIDs = append(lb.InstanceIDs, aws.ToString(in.InstanceId))
			}
			lbs = append(lbs, lb)
		}
	}); err != nil {
		skip("elasticloadbalancing:DescribeLoadBalancers", err)
		return
	}
	idx := map[string]int{}
	var idle []string
	for i := range lbs {
		if lbs[i].Instances > 0 {
			out, err := c.DescribeInstanceHealth(ctx, &elb.DescribeInstanceHealthInput{LoadBalancerName: aws.String(lbs[i].Name)})
			if err != nil {
				lbs[i].InService = -1
				skip("elasticloadbalancing:DescribeInstanceHealth", err)
				continue
			}
			for _, s := range out.InstanceStates {
				if aws.ToString(s.State) == "InService" {
					lbs[i].InService++
				}
			}
		}
		if lbs[i].InService == 0 {
			idx[lbs[i].Name] = i
			idle = append(idle, lbs[i].Name)
		}
	}
	for from := 0; from < len(idle); from += 20 {
		part := idle[from:min(from+20, len(idle))]
		out, err := c.DescribeTags(ctx, &elb.DescribeTagsInput{LoadBalancerNames: part})
		if err != nil {
			for _, n := range part {
				lbs[idx[n]].TagsUnknown = true
			}
			skip("elasticloadbalancing:DescribeTags", err)
			continue
		}
		for _, d := range out.TagDescriptions {
			if i, ok := idx[aws.ToString(d.LoadBalancerName)]; ok {
				var kv [][2]string
				for _, tg := range d.Tags {
					kv = append(kv, [2]string{aws.ToString(tg.Key), aws.ToString(tg.Value)})
				}
				lbs[i].Kubernetes = kubernetesCluster(kv)
			}
		}
	}
	r.ClassicLBs = lbs
	r.exists("LoadBalancerName", liveIDs(lbs, func(lb ClassicLB) (string, bool) { return lb.Name, true }))
}
