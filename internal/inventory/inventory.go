// Package inventory reads, once per region, everything the checks need. Every
// call is a read-only Describe*/List* call. A call that fails (for example, no
// permission) is recorded as a Skip and the scan carries on.
package inventory

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/pricing"

	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
)

// LoadBalancer is an ALB/NLB/GWLB with its target counts already resolved.
type LoadBalancer struct {
	ARN          string
	Name         string
	Type         string
	Scheme       string
	TargetGroups int
	Targets      int
	Created      *time.Time

	VPC             string
	TargetGroupARNs []string
	TargetInstances []string   // EC2 instances registered with its target groups
	TargetIPs       []TargetIP // targets registered by IP address
	Kubernetes      string     // the Kubernetes cluster its tags name ("?" if they don't say which), "" if none
	TagsUnknown     bool       // its tags couldn't be read

	// Application Load Balancers with no targets: how many listeners redirect
	// or return a fixed response (they answer users without a target).
	Responds         int
	ListenersUnknown bool // its listeners or rules couldn't be read
}

// ELBListenersAPI reads load balancers' listeners and rules. Only their
// actions' types are kept: an OIDC login action can include a client secret
// (AWS doesn't document whether it's returned) and extra parameters, and a
// fixed response a body.
type ELBListenersAPI interface {
	elb.DescribeListenersAPIClient
	elb.DescribeRulesAPIClient
}

// ELBTagsAPI reads load balancer tags (API version 2).
type ELBTagsAPI interface {
	DescribeTags(context.Context, *elb.DescribeTagsInput, ...func(*elb.Options)) (*elb.DescribeTagsOutput, error)
}

// Skip records something that could not be read.
type Skip struct {
	Region string `json:"region"`
	What   string `json:"what"`
	Error  string `json:"error"`
}

// TargetIP is a load balancer target registered by IP address (canonical
// form). VPC is its target group's VPC, or "" for a target outside it
// (AvailabilityZone "all": a peered VPC or on premises).
type TargetIP struct{ IP, VPC string }

// Region is the read-only snapshot of one region.
type Region struct {
	Name           string
	Volumes        []ec2types.Volume
	Instances      []ec2types.Instance
	Addresses      []ec2types.Address
	Snapshots      []ec2types.Snapshot
	Images         []ec2types.Image
	NatGateways    []ec2types.NatGateway
	VpcEndpoints   []ec2types.VpcEndpoint
	LogGroups      []logstypes.LogGroup
	LoadBalancers  []LoadBalancer
	Secrets        []smtypes.SecretListEntry
	Repos          []Repo
	Functions      []lambdatypes.FunctionConfiguration
	CacheClusters  []CacheCluster
	Tables         []Table
	FastRestores   []FastRestore
	RecoveryPoints []RecoveryPoint
	FileSystems    []FileSystem
	Streams        []Stream
	Buckets        []Bucket      // set by the caller from LoadBuckets (S3 is listed once per account)
	Usage          *Usage        // paid plans only; nil on the free plan
	Prices         *pricing.Book // paid plans: this region\'s prices from the AWS Price List; nil = built-in
	Skipped        []Skip

	KinesisOnDemandBilling KinesisOnDemandBilling

	// Long-tail free checks (longtail.go).
	ImageRefs          map[string]bool // AMI IDs that something references (EC2 DescribeImageReferences); nil = unknown
	LaunchConfigASGs   int             // Auto Scaling groups on a launch configuration (-1 = unknown)
	Hosts              []Host
	Offered            map[string]map[string]bool // Availability Zone -> the asked-about instance types offered there; nil = unknown
	Accelerators       []Accelerator              // the "global" pseudo-region of the Global Accelerator pass (us-west-2)
	Keys               []KMSKey
	DBSnapshots        []DBSnapshot
	EKSClusters        []EKSInfo
	EKSListed          bool                // EKS ListClusters answered in this region
	EKSNames           []string            // every cluster it returned, even one whose details couldn't be read
	EKSAutoScaling     map[string]int      // EKS cluster -> Auto Scaling groups tagged as its nodes (self-managed, even at 0)
	NATRouted          map[string]bool     // route targets: "nat:<id>", "eni:<id>", "ip:<vpc>/<addr>"; nil = unknown
	EndpointRoutes     map[string][]string // VPC endpoint ID -> the routes that target it ("rtb-1 0.0.0.0/0"); nil = unknown
	Trails             []Trail             // trails that log in this region (cloudtrail.go)
	RestStages         []RestStage         // REST API stages and their caches (apigateway.go)
	RestAPIMetricNames map[string]int      // the ApiName each REST API's metrics carry -> how many APIs share it; nil = unknown
	DBInstances        []DBInstance        // RDS DB instances (read with the snapshots)
	DBClusters         []DBCluster         // RDS DB clusters: Aurora, Multi-AZ, Neptune, DocumentDB (read with the snapshots; nil = not read)
	ClassicLBs         []ClassicLB
	ASGLoadBalancers   map[string][]string     // "clb:<name>" or "tg:<target group ARN>" -> Auto Scaling groups that register instances with it (listed or as a traffic source)
	ASGUnknown         bool                    // the Auto Scaling groups couldn't be read
	ASGNames           []string                // the Auto Scaling groups that exist (none when ASGUnknown)
	ASGLaunch          map[string]LaunchSource // Auto Scaling group -> what launches its instances
	EC2Fleets          []EC2Fleet              // nil = not read (ec2-detailed-monitoring)
	SpotFleets         []SpotFleet             // nil = not read; ID and state only (UserData dropped)
	BatchCEs           []BatchCE               // nil = not read
	// Unjudged: "check|key" of resources that exist but a check couldn't
	// judge this run (a group scaled to zero, a service it can't price): the
	// report parks their findings, so going away isn't a fix. Checks of one
	// region run one after another (checks.Run), so they append safely.
	Unjudged []string

	// Account-wide (the "global" pseudo-region, LoadGlobal).
	HealthChecks             []HealthCheck
	HealthCheckUsesUnknown   bool         // the record sets couldn't all be read: no health check is known unused
	HealthCheckAlarmsUnknown bool         // the CloudWatch alarms couldn't be read
	TrafficPolicies          bool         // traffic policy instances exist (or couldn't be listed)
	Zones                    []HostedZone // the account's hosted zones (the global pass)
	ZonesListed              bool         // ListHostedZones answered in full

	Alarms       []Alarm                    // the region's CloudWatch metric alarms (orphan-alarm)
	AlarmsListed bool                       // DescribeAlarms answered in full
	Exists       map[string]map[string]bool // metric dimension ("InstanceId") -> the IDs or names that exist, for each resource type read in full

	CapacityReservations []CapacityReservation // EC2 On-Demand Capacity Reservations, shared ones included
	DMS                  *DMS
	PCA                  *PCA        // ACM Private CAs (nil = not read)
	WorkSpaces           *WorkSpaces // nil = list failed or not read
	CapacityListed       bool        // DescribeCapacityReservations answered in full
}

// EC2API is the subset of the EC2 client costhound uses.
type EC2API interface {
	ec2.DescribeVolumesAPIClient
	ec2.DescribeInstancesAPIClient
	ec2.DescribeSnapshotsAPIClient
	ec2.DescribeImagesAPIClient
	ec2.DescribeNatGatewaysAPIClient
	ec2.DescribeVpcEndpointsAPIClient
	DescribeAddresses(context.Context, *ec2.DescribeAddressesInput, ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
}

// LogsAPI is the subset of the CloudWatch Logs client costhound uses.
type LogsAPI interface {
	cloudwatchlogs.DescribeLogGroupsAPIClient
}

// ELBAPI is the subset of the ELBv2 client costhound uses.
type ELBAPI interface {
	elb.DescribeLoadBalancersAPIClient
	elb.DescribeTargetGroupsAPIClient
	DescribeTargetHealth(context.Context, *elb.DescribeTargetHealthInput, ...func(*elb.Options)) (*elb.DescribeTargetHealthOutput, error)
}

// Clients are the per-region API clients.
type Clients struct {
	EC2  EC2API
	Logs LogsAPI
	ELB  ELBAPI
	// Optional: nil skips those checks.
	Secrets     SecretsAPI
	ECR         ECRAPI
	Lambda      LambdaAPI
	ElastiCache ElastiCacheAPI
	DynamoDB    DynamoDBAPI
	AutoScaling AutoScalingAPI
	FastRestore ec2.DescribeFastSnapshotRestoresAPIClient
	Backup      BackupAPI
	EFS         EFSAPI
	Kinesis     KinesisAPI

	// Long-tail free checks (longtail.go).
	ImageRefs     ImageRefsAPI
	ASG           ASGAPI
	Hosts         ec2.DescribeHostsAPIClient
	Offerings     ec2.DescribeInstanceTypeOfferingsAPIClient
	KMS           KMSAPI
	RDS           RDSSnapshotAPI
	EKS           EKSFreeAPI
	Routes        RouteTablesAPI
	Classic       ClassicLBAPI
	ELBTags       ELBTagsAPI
	ELBRules      ELBListenersAPI
	APIGateway    APIGatewayAPI
	CloudTrail    CloudTrailAPI
	Alarms        AlarmsAPI // CloudWatch alarms (orphan-alarm)
	Capacity      ec2.DescribeCapacityReservationsAPIClient
	Fleets        ec2.DescribeFleetsAPIClient
	SpotFleets    ec2.DescribeSpotFleetRequestsAPIClient
	Batch         BatchAPI
	DMS           DMSAPI
	PCA           PCAAPI
	RolesAnywhere RolesAnywhereAPI
	ACM           ACMAPI
	WorkSpaces    WorkSpacesAPI
}

// Load reads one region. It never returns an error: failures land in Skipped.
// account is the scanned account's ID: snapshots and AMIs owned by anyone else
// are dropped even if the API returned them, so costhound can never suggest
// deleting something the account doesn't own.
func Load(ctx context.Context, region, account string, c Clients) *Region {
	r := &Region{Name: region}
	skip := func(what string, err error) {
		r.Skipped = append(r.Skipped, Skip{Region: region, What: what, Error: ShortError(err)})
	}

	p1 := ec2.NewDescribeVolumesPaginator(c.EC2, &ec2.DescribeVolumesInput{})
	if err := drain(ctx, p1.HasMorePages, p1.NextPage, func(o *ec2.DescribeVolumesOutput) {
		r.Volumes = append(r.Volumes, o.Volumes...)
	}); err != nil {
		skip("ec2:DescribeVolumes", err)
	} else {
		r.exists("VolumeId", liveIDs(r.Volumes, func(v ec2types.Volume) (string, bool) {
			return aws.ToString(v.VolumeId), v.State != ec2types.VolumeStateDeleting && v.State != ec2types.VolumeStateDeleted
		}))
	}

	p2 := ec2.NewDescribeInstancesPaginator(c.EC2, &ec2.DescribeInstancesInput{})
	if err := drain(ctx, p2.HasMorePages, p2.NextPage, func(o *ec2.DescribeInstancesOutput) {
		for _, res := range o.Reservations {
			r.Instances = append(r.Instances, res.Instances...)
		}
	}); err != nil {
		skip("ec2:DescribeInstances", err)
	} else {
		r.exists("InstanceId", liveIDs(r.Instances, func(in ec2types.Instance) (string, bool) {
			gone := in.State != nil && (in.State.Name == ec2types.InstanceStateNameTerminated || in.State.Name == ec2types.InstanceStateNameShuttingDown)
			return aws.ToString(in.InstanceId), !gone
		}))
	}

	if out, err := c.EC2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{}); err != nil {
		skip("ec2:DescribeAddresses", err)
	} else {
		r.Addresses = out.Addresses
	}

	// OwnerIds=self is essential: without it EC2 returns every public snapshot.
	p3 := ec2.NewDescribeSnapshotsPaginator(c.EC2, &ec2.DescribeSnapshotsInput{OwnerIds: []string{"self"}})
	if err := drain(ctx, p3.HasMorePages, p3.NextPage, func(o *ec2.DescribeSnapshotsOutput) {
		r.Snapshots = append(r.Snapshots, o.Snapshots...)
	}); err != nil {
		skip("ec2:DescribeSnapshots", err)
	}

	// Our own AMIs, including deprecated and disabled ones: they all keep their
	// snapshots alive, so those snapshots must not be reported as deletable.
	p4 := ec2.NewDescribeImagesPaginator(c.EC2, &ec2.DescribeImagesInput{
		Owners:            []string{"self"},
		IncludeDeprecated: aws.Bool(true),
		IncludeDisabled:   aws.Bool(true),
	})
	if err := drain(ctx, p4.HasMorePages, p4.NextPage, func(o *ec2.DescribeImagesOutput) {
		r.Images = append(r.Images, o.Images...)
	}); err != nil {
		skip("ec2:DescribeImages", err)
	}

	p5 := ec2.NewDescribeNatGatewaysPaginator(c.EC2, &ec2.DescribeNatGatewaysInput{})
	if err := drain(ctx, p5.HasMorePages, p5.NextPage, func(o *ec2.DescribeNatGatewaysOutput) {
		r.NatGateways = append(r.NatGateways, o.NatGateways...)
	}); err != nil {
		skip("ec2:DescribeNatGateways", err)
	} else {
		r.exists("NatGatewayId", liveIDs(r.NatGateways, func(n ec2types.NatGateway) (string, bool) {
			s := n.State
			return aws.ToString(n.NatGatewayId), s != ec2types.NatGatewayStateDeleted && s != ec2types.NatGatewayStateDeleting && s != ec2types.NatGatewayStateFailed
		}))
	}

	p6 := ec2.NewDescribeVpcEndpointsPaginator(c.EC2, &ec2.DescribeVpcEndpointsInput{})
	if err := drain(ctx, p6.HasMorePages, p6.NextPage, func(o *ec2.DescribeVpcEndpointsOutput) {
		r.VpcEndpoints = append(r.VpcEndpoints, o.VpcEndpoints...)
	}); err != nil {
		skip("ec2:DescribeVpcEndpoints", err)
	}

	if c.Logs != nil {
		p7 := cloudwatchlogs.NewDescribeLogGroupsPaginator(c.Logs, &cloudwatchlogs.DescribeLogGroupsInput{})
		if err := drain(ctx, p7.HasMorePages, p7.NextPage, func(o *cloudwatchlogs.DescribeLogGroupsOutput) {
			r.LogGroups = append(r.LogGroups, o.LogGroups...)
		}); err != nil {
			skip("logs:DescribeLogGroups", err)
		} else {
			r.exists("LogGroupName", liveIDs(r.LogGroups, func(g logstypes.LogGroup) (string, bool) { return aws.ToString(g.LogGroupName), true }))
		}
	}

	if c.ELB != nil {
		if lbs, what, err := loadBalancers(ctx, c.ELB); err != nil {
			skip(what, err)
		} else {
			r.LoadBalancers = lbs
			// CloudWatch names one by its ARN's end: app/<name>/<id>, net/..., gwy/...
			r.exists("LoadBalancer", liveIDs(lbs, func(lb LoadBalancer) (string, bool) {
				_, dim, ok := strings.Cut(lb.ARN, ":loadbalancer/")
				return dim, ok
			}))
			if c.ELBTags != nil {
				loadLBTags(ctx, r.LoadBalancers, c.ELBTags, skip)
			}
			if c.ELBRules != nil {
				loadLBListeners(ctx, r.LoadBalancers, c.ELBRules, skip)
			}
		}
	}
	loadExtras(ctx, r, c, skip)
	loadLongTail(ctx, r, c, skip)
	if c.Alarms != nil {
		loadAlarms(ctx, r, c.Alarms, skip)
	}
	if c.Capacity != nil {
		loadCapacityReservations(ctx, r, c.Capacity, account, skip)
	}
	if c.Fleets != nil {
		loadEC2Fleets(ctx, r, c.Fleets, skip)
	}
	if c.SpotFleets != nil {
		loadSpotFleets(ctx, r, c.SpotFleets, skip)
	}
	if c.Batch != nil {
		loadBatchCEs(ctx, r, c.Batch, skip)
	}
	if c.DMS != nil {
		loadDMS(ctx, r, c.DMS, skip)
	}
	if c.PCA != nil {
		loadPCA(ctx, r, c.PCA, c.RolesAnywhere, c.ACM, skip)
	}
	if c.WorkSpaces != nil {
		loadWorkSpaces(ctx, r, c.WorkSpaces, skip)
	}
	if account != "" {
		r.Snapshots = ownedBy(r.Snapshots, account, func(s ec2types.Snapshot) string { return aws.ToString(s.OwnerId) })
		r.Images = ownedBy(r.Images, account, func(i ec2types.Image) string { return aws.ToString(i.OwnerId) })
		r.Hosts = ownedBy(r.Hosts, account, func(h Host) string { return h.Owner })
	}
	if c.ImageRefs != nil && len(r.Images) > 0 {
		if err := loadImageRefs(ctx, r, c.ImageRefs); err != nil {
			skip("ec2:DescribeImageReferences", err)
		}
	}
	return r
}

// ownedBy keeps items whose owner is the account (or unknown).
func ownedBy[T any](items []T, account string, owner func(T) string) []T {
	out := items[:0:0]
	for _, it := range items {
		if o := owner(it); o == "" || o == account {
			out = append(out, it)
		}
	}
	return out
}

func loadBalancers(ctx context.Context, c ELBAPI) ([]LoadBalancer, string, error) {
	var lbs []LoadBalancer
	idx := map[string]int{}
	p8 := elb.NewDescribeLoadBalancersPaginator(c, &elb.DescribeLoadBalancersInput{})
	if err := drain(ctx, p8.HasMorePages, p8.NextPage, func(o *elb.DescribeLoadBalancersOutput) {
		for _, lb := range o.LoadBalancers {
			idx[aws.ToString(lb.LoadBalancerArn)] = len(lbs)
			lbs = append(lbs, LoadBalancer{
				ARN:     aws.ToString(lb.LoadBalancerArn),
				Name:    aws.ToString(lb.LoadBalancerName),
				Type:    string(lb.Type),
				Scheme:  string(lb.Scheme),
				Created: lb.CreatedTime,
				VPC:     aws.ToString(lb.VpcId),
			})
		}
	}); err != nil {
		return nil, "elasticloadbalancing:DescribeLoadBalancers", err
	}
	if len(lbs) == 0 {
		return nil, "", nil
	}

	var tgErr error
	tgp := elb.NewDescribeTargetGroupsPaginator(c, &elb.DescribeTargetGroupsInput{})
	err := drain(ctx, tgp.HasMorePages, tgp.NextPage, func(o *elb.DescribeTargetGroupsOutput) {
		for _, tg := range o.TargetGroups {
			if len(tg.LoadBalancerArns) == 0 || tgErr != nil {
				continue
			}
			h, err := c.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{TargetGroupArn: tg.TargetGroupArn})
			if err != nil {
				tgErr = err
				continue
			}
			for _, arn := range tg.LoadBalancerArns {
				if i, ok := idx[arn]; ok {
					for _, d := range h.TargetHealthDescriptions {
						if d.Target == nil {
							continue
						}
						id := aws.ToString(d.Target.Id)
						if strings.HasPrefix(id, "i-") {
							lbs[i].TargetInstances = append(lbs[i].TargetInstances, id)
						} else if ip, err := netip.ParseAddr(id); err == nil {
							vpc := aws.ToString(tg.VpcId)
							if aws.ToString(d.Target.AvailabilityZone) == "all" {
								vpc = "" // outside the target group's VPC: a peered VPC or on premises
							}
							lbs[i].TargetIPs = append(lbs[i].TargetIPs, TargetIP{IP: ip.Unmap().String(), VPC: vpc})
						}
					}
					lbs[i].TargetGroupARNs = append(lbs[i].TargetGroupARNs, aws.ToString(tg.TargetGroupArn))
					lbs[i].TargetGroups++
					lbs[i].Targets += len(h.TargetHealthDescriptions)
				}
			}
		}
	})
	if err != nil {
		return nil, "elasticloadbalancing:DescribeTargetGroups", err
	}
	if tgErr != nil {
		return nil, "elasticloadbalancing:DescribeTargetHealth", tgErr
	}
	return lbs, "", nil
}

// loadLBListeners reads the listeners and rules of the Application Load
// Balancers with no targets: a redirect or a fixed response still answers
// users without one. Only whether an action redirects or answers is kept.
// After a failed read the rest stay unknown: one skip line, not one each.
func loadLBListeners(ctx context.Context, lbs []LoadBalancer, c ELBListenersAPI, skip func(string, error)) {
	answers := func(as []elbtypes.Action) bool {
		for _, a := range as {
			if a.Type == elbtypes.ActionTypeEnumRedirect || a.Type == elbtypes.ActionTypeEnumFixedResponse {
				return true
			}
		}
		return false
	}
	failed := false
	for i := range lbs {
		lb := &lbs[i]
		if lb.Type != "application" || lb.Targets > 0 {
			continue
		}
		if failed {
			lb.ListenersUnknown = true
			continue
		}
		var listeners []string
		n := 0
		lp := elb.NewDescribeListenersPaginator(c, &elb.DescribeListenersInput{LoadBalancerArn: aws.String(lb.ARN)})
		if err := drain(ctx, lp.HasMorePages, lp.NextPage, func(o *elb.DescribeListenersOutput) {
			for _, l := range o.Listeners {
				if answers(l.DefaultActions) {
					n++
				} else {
					listeners = append(listeners, aws.ToString(l.ListenerArn))
				}
			}
		}); err != nil {
			lb.ListenersUnknown, failed = true, true
			skip("elasticloadbalancing:DescribeListeners", err)
			continue
		}
		for _, arn := range listeners {
			found := false
			rp := elb.NewDescribeRulesPaginator(c, &elb.DescribeRulesInput{ListenerArn: aws.String(arn)})
			if err := drain(ctx, rp.HasMorePages, rp.NextPage, func(o *elb.DescribeRulesOutput) {
				for _, r := range o.Rules {
					found = found || answers(r.Actions)
				}
			}); err != nil {
				lb.ListenersUnknown, failed = true, true
				skip("elasticloadbalancing:DescribeRules", err)
				break
			}
			if found {
				n++
			}
		}
		lb.Responds = n
	}
}

// loadLBTags reads the tags of the load balancers with no targets (the only
// ones a check judges by that), 20 at a time, to see whether Kubernetes made
// them: their targets come and go with its nodes and pods.
func loadLBTags(ctx context.Context, lbs []LoadBalancer, c ELBTagsAPI, skip func(string, error)) {
	idx := map[string]int{}
	var arns []string
	for i, lb := range lbs {
		if lb.Targets == 0 {
			idx[lb.ARN] = i
			arns = append(arns, lb.ARN)
		}
	}
	for from := 0; from < len(arns); from += 20 {
		part := arns[from:min(from+20, len(arns))]
		out, err := c.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: part})
		if err != nil {
			for _, a := range part {
				lbs[idx[a]].TagsUnknown = true
			}
			skip("elasticloadbalancing:DescribeTags", err)
			continue
		}
		for _, d := range out.TagDescriptions {
			if i, ok := idx[aws.ToString(d.ResourceArn)]; ok {
				var kv [][2]string
				for _, tg := range d.Tags {
					kv = append(kv, [2]string{aws.ToString(tg.Key), aws.ToString(tg.Value)})
				}
				lbs[i].Kubernetes = kubernetesCluster(kv)
			}
		}
	}
}

// kubernetesCluster says which Kubernetes cluster made a load balancer, from
// the tags its in-tree cloud provider or the AWS Load Balancer Controller
// set: the cluster's name, "?" when only the service is named, "" if none.
func kubernetesCluster(tags [][2]string) string {
	who := ""
	for _, kv := range tags {
		switch k, v := kv[0], kv[1]; {
		case strings.HasPrefix(k, "kubernetes.io/cluster/") && v == "owned": // "shared": the user's, lent to the cluster
			return strings.TrimPrefix(k, "kubernetes.io/cluster/")
		case (k == "elbv2.k8s.aws/cluster" || k == "eks:eks-cluster-name" || k == "KubernetesCluster") && v != "":
			return v
		case k == "kubernetes.io/service-name", k == "service.k8s.aws/stack", k == "ingress.k8s.aws/stack", k == "service.k8s.aws/resource":
			who = "?"
		}
	}
	return who
}

// drain reads every page of an AWS SDK v2 paginator. Pass p.HasMorePages and
// p.NextPage; Go infers the page type from NextPage.
func drain[O, Opt any](ctx context.Context, more func() bool, next func(context.Context, ...func(*Opt)) (*O, error), fn func(*O)) error {
	for more() {
		out, err := next(ctx)
		if err != nil {
			return err
		}
		fn(out)
	}
	return nil
}

// ShortError turns an AWS error into its error code (e.g. AccessDenied), which
// is all a reader needs; anything else is returned as-is.
func ShortError(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return err.Error()
}

// NodeCluster is the Kubernetes cluster an EC2 resource's tags say it belongs
// to (kubernetes.io/cluster/<name> = owned, else KubernetesCluster,
// eks:cluster-name or eks:eks-cluster-name), and whether they look like kops,
// which EKS never lists. "shared" is the user's resource, lent to a cluster.
func NodeCluster(tags []ec2types.Tag) (name string, kops bool) {
	for _, t := range tags {
		k, v := aws.ToString(t.Key), aws.ToString(t.Value)
		switch {
		case strings.HasPrefix(k, "kops.k8s.io/"):
			kops = true
		case strings.HasPrefix(k, "kubernetes.io/cluster/") && v == "owned":
			name = strings.TrimPrefix(k, "kubernetes.io/cluster/")
		case (k == "KubernetesCluster" || k == "eks:cluster-name" || k == "eks:eks-cluster-name") && v != "" && name == "":
			name = v
		}
	}
	return name, kops
}
