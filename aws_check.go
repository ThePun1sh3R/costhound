package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/aws/aws-sdk-go-v2/service/costoptimizationhub"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

const (
	awsCheckOK   = "OK"
	awsCheckNOTE = "NOTE"
	awsCheckFIX  = "FIX"
)

// AWSCheckItem is one line of the AWS setup checklist.
type AWSCheckItem struct {
	Status string `json:"status"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// AWSCheckReport is the -check result (access only; no findings).
type AWSCheckReport struct {
	Cloud string         `json:"cloud"`
	Plan  string         `json:"plan"`
	Ready bool           `json:"ready"`
	Items []AWSCheckItem `json:"items"`
}

// AWSCheckOpts configures an access check.
type AWSCheckOpts struct {
	Profile string
	Regions string // raw -regions flag
	OrgScan bool
	License license.Status
	OrgID   string // AWS Organization ID when the key is org-scoped
}

type awsProbe struct {
	name       string
	action     string
	policyFile string
	strictSkip bool // lambda: NOTE when denied if other probes passed
	run        func(context.Context, aws.Config, string) error
}

// awsCheck runs the read-only setup preflight; tests may replace it.
var awsCheck = func(ctx context.Context, cfg aws.Config, plan string, opts AWSCheckOpts) (*AWSCheckReport, error) {
	return runAWSCheck(ctx, cfg, plan, opts)
}

func runAWSCheck(ctx context.Context, cfg aws.Config, plan string, opts AWSCheckOpts) (*AWSCheckReport, error) {
	rep := &AWSCheckReport{Cloud: "aws", Plan: plan, Items: []AWSCheckItem{}}

	who, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		rep.Items = append(rep.Items, AWSCheckItem{
			Status: awsCheckFIX, Name: "credentials",
			Detail: inventory.ShortError(err),
			Fix:    awsCredentialsFix(opts.Profile, err),
		})
		rep.Ready = false
		return rep, nil
	}
	account := aws.ToString(who.Account)
	arn := aws.ToString(who.Arn)
	rep.Items = append(rep.Items, AWSCheckItem{
		Status: awsCheckOK, Name: "account",
		Detail: fmt.Sprintf("%s (%s)", account, arn),
	})

	regions, regDetail, regErr := awsCheckRegions(ctx, cfg, opts.Regions)
	rep.Items = append(rep.Items, classifyRegionItem(regDetail, regErr))
	if len(regions) == 0 {
		rep.Ready = !awsCheckHasFIX(rep.Items)
		lic := opts.License.ForAccount(account, opts.OrgID)
		appendAWSLicenseNote(rep, lic.Plan, lic.Problem)
		return rep, nil
	}
	region := regions[0]

	type probeRun struct {
		p   awsProbe
		err error
	}
	var runs []probeRun
	for _, p := range awsCheckProbes() {
		runs = append(runs, probeRun{p: p, err: p.run(ctx, cfg, region)})
	}
	for _, p := range awsCheckGlobalProbes() {
		runs = append(runs, probeRun{p: p, err: p.run(ctx, cfg, region)})
	}
	spot := awsSpotProbe()
	runs = append(runs, probeRun{p: spot, err: spot.run(ctx, cfg, region)})

	othersOK := true
	for _, r := range runs {
		if r.p.strictSkip {
			continue
		}
		if classifyAWSProbe(r.p, r.err, false).Status == awsCheckFIX {
			othersOK = false
			break
		}
	}
	for _, r := range runs {
		otherOK := r.p.strictSkip && othersOK
		rep.Items = append(rep.Items, classifyAWSProbe(r.p, r.err, otherOK))
	}

	if opts.OrgScan {
		orgProbe := awsProbe{
			name: "organizations", action: "organizations:DescribeOrganization", policyFile: "iam-policy.json",
		}
		_, err := organizations.NewFromConfig(cfg).DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
		rep.Items = append(rep.Items, classifyAWSProbe(orgProbe, err, false))
	}

	rep.Ready = !awsCheckHasFIX(rep.Items)
	lic := opts.License.ForAccount(account, opts.OrgID)
	appendAWSLicenseNote(rep, lic.Plan, lic.Problem)
	return rep, nil
}

func awsCheckProbes() []awsProbe {
	one := int32(1)
	return []awsProbe{
		{"ec2", "ec2:DescribeVolumes", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			c := regionalCfg(cfg, region)
			_, err := ec2.NewFromConfig(c).DescribeVolumes(ctx, &ec2.DescribeVolumesInput{MaxResults: aws.Int32(5)})
			return err
		}},
		{"rds", "rds:DescribeDBInstances", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := rds.NewFromConfig(regionalCfg(cfg, region)).DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{MaxRecords: aws.Int32(20)})
			return err
		}},
		{"elasticloadbalancing", "elasticloadbalancing:DescribeLoadBalancers", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := elbv2.NewFromConfig(regionalCfg(cfg, region)).DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{PageSize: aws.Int32(1)})
			return err
		}},
		{"cloudwatch", "cloudwatch:ListMetrics", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := cloudwatch.NewFromConfig(regionalCfg(cfg, region)).ListMetrics(ctx, &cloudwatch.ListMetricsInput{Namespace: aws.String("AWS/EC2"), MetricName: aws.String("CPUUtilization")})
			return err
		}},
		{"logs", "logs:DescribeLogGroups", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := cloudwatchlogs.NewFromConfig(regionalCfg(cfg, region)).DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{Limit: &one})
			return err
		}},
		{"s3", "s3:ListAllMyBuckets", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, _ string) error {
			_, err := s3.NewFromConfig(cfg).ListBuckets(ctx, &s3.ListBucketsInput{MaxBuckets: aws.Int32(1)})
			return err
		}},
		{"lambda", "lambda:ListFunctions", "iam-policy.json", true, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := lambda.NewFromConfig(regionalCfg(cfg, region)).ListFunctions(ctx, &lambda.ListFunctionsInput{MaxItems: &one})
			return err
		}},
		{"dynamodb", "dynamodb:ListTables", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := dynamodb.NewFromConfig(regionalCfg(cfg, region)).ListTables(ctx, &dynamodb.ListTablesInput{Limit: &one})
			return err
		}},
		{"elasticache", "elasticache:DescribeCacheClusters", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			// ElastiCache takes 20 to 100 records per page; 1 is InvalidParameterValue.
			twenty := int32(20)
			_, err := elasticache.NewFromConfig(regionalCfg(cfg, region)).DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{MaxRecords: &twenty})
			return err
		}},
		{"eks", "eks:ListClusters", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := eks.NewFromConfig(regionalCfg(cfg, region)).ListClusters(ctx, &eks.ListClustersInput{MaxResults: &one})
			return err
		}},
		{"route53", "route53:ListHostedZones", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, _ string) error {
			_, err := route53.NewFromConfig(cfg).ListHostedZones(ctx, &route53.ListHostedZonesInput{MaxItems: &one})
			return err
		}},
		{"tag", "tag:GetResources", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := resourcegroupstaggingapi.NewFromConfig(regionalCfg(cfg, region)).GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
				ResourcesPerPage: &one,
			})
			return err
		}},
	}
}

func awsCheckGlobalProbes() []awsProbe {
	one := int32(1)
	return []awsProbe{
		{"pricing", "pricing:GetProducts", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, _ string) error {
			c := regionalCfg(cfg, "us-east-1")
			_, err := pricing.NewFromConfig(c).GetProducts(ctx, &pricing.GetProductsInput{
				ServiceCode: aws.String("AmazonEC2"),
				MaxResults:  &one,
				Filters: []pricingtypes.Filter{
					{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("instanceType"), Value: aws.String("t3.micro")},
				},
			})
			return err
		}},
		{"compute-optimizer", "compute-optimizer:GetEnrollmentStatus", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, _ string) error {
			out, err := computeoptimizer.NewFromConfig(globalCfg(cfg)).GetEnrollmentStatus(ctx, &computeoptimizer.GetEnrollmentStatusInput{})
			if err != nil {
				return err
			}
			if out.Status != cotypes.StatusActive {
				return fmt.Errorf("%w", errComputeOptimizerOff)
			}
			return nil
		}},
		{"cost-optimization-hub", "cost-optimization-hub:ListRecommendations", "iam-policy.json", false, func(ctx context.Context, cfg aws.Config, _ string) error {
			_, err := costoptimizationhub.NewFromConfig(globalCfg(cfg)).ListRecommendations(ctx, &costoptimizationhub.ListRecommendationsInput{
				MaxResults: &one,
			})
			return err
		}},
	}
}

func awsSpotProbe() awsProbe {
	return awsProbe{
		name: "ec2-spot", action: "ec2:DescribeSpotPriceHistory", policyFile: "iam-policy-pro-services.json", strictSkip: false,
		run: func(ctx context.Context, cfg aws.Config, region string) error {
			_, err := ec2.NewFromConfig(regionalCfg(cfg, region)).DescribeSpotPriceHistory(ctx, &ec2.DescribeSpotPriceHistoryInput{
				MaxResults:    aws.Int32(5),
				InstanceTypes: []ec2types.InstanceType{ec2types.InstanceTypeT3Micro},
			})
			return err
		},
	}
}

var errComputeOptimizerOff = errors.New("compute optimizer is off")

func regionalCfg(cfg aws.Config, region string) aws.Config {
	c := cfg.Copy()
	c.Region = region
	return c
}

func globalCfg(cfg aws.Config) aws.Config {
	return regionalCfg(cfg, "us-east-1")
}

func awsCheckRegions(ctx context.Context, cfg aws.Config, flag string) ([]string, string, error) {
	if strings.TrimSpace(flag) != "" {
		regions, err := pickRegions(ctx, cfg, flag)
		if err != nil {
			return nil, "", err
		}
		return regions, fmt.Sprintf("%d region(s) from -regions", len(regions)), nil
	}
	regions, err := pickRegions(ctx, cfg, "")
	if err != nil {
		return nil, "", err
	}
	return regions, fmt.Sprintf("%d enabled region(s)", len(regions)), nil
}

func classifyRegionItem(detail string, err error) AWSCheckItem {
	name := "regions"
	if err != nil {
		code := inventory.ShortError(err)
		if report.IsDeniedCode(code) {
			return AWSCheckItem{
				Status: awsCheckFIX, Name: name,
				Detail: "ec2:DescribeRegions denied",
				Fix:    "attach iam-policy.json",
			}
		}
		if inventory.IsNoSuchHost(err) {
			return AWSCheckItem{Status: awsCheckNOTE, Name: name, Detail: "EC2 endpoint not reachable"}
		}
		return AWSCheckItem{Status: awsCheckNOTE, Name: name, Detail: code}
	}
	return AWSCheckItem{Status: awsCheckOK, Name: name, Detail: detail}
}

func classifyAWSProbe(p awsProbe, err error, otherProbesOK bool) AWSCheckItem {
	if err == nil {
		return AWSCheckItem{Status: awsCheckOK, Name: p.name, Detail: "readable"}
	}
	if errors.Is(err, errComputeOptimizerOff) {
		return AWSCheckItem{
			Status: awsCheckNOTE, Name: p.name,
			Detail: "Compute Optimizer is off; turn it on free with: aws compute-optimizer update-enrollment-status --status Active",
		}
	}
	var api smithy.APIError
	if errors.As(err, &api) && strings.Contains(strings.ToLower(api.ErrorMessage()), "not enrolled") {
		return AWSCheckItem{
			Status: awsCheckNOTE, Name: p.name,
			Detail: "Cost Optimization Hub is off; enable it in the console (Billing → Cost Optimization Hub)",
		}
	}
	code := inventory.ShortError(err)
	if inventory.IsNoSuchHost(err) {
		return AWSCheckItem{
			Status: awsCheckNOTE, Name: p.name,
			Detail: inventory.ServiceLabel(p.action) + " isn't offered or reachable in this region",
		}
	}
	if code == "OptInRequired" {
		return AWSCheckItem{Status: awsCheckNOTE, Name: p.name, Detail: "service requires opt-in in this region"}
	}
	if report.IsDeniedCode(code) {
		if deniedBySCP(err) {
			return AWSCheckItem{
				Status: awsCheckNOTE, Name: p.name,
				Detail: p.action + " is blocked by a service control policy (your AWS Organization, or the AWS Free plan's limits). No IAM policy can allow it; the checks that need it are skipped. Ask whoever runs the organization if you want them.",
			}
		}
		if p.strictSkip && otherProbesOK {
			return AWSCheckItem{
				Status: awsCheckNOTE, Name: p.name,
				Detail: p.action + " denied (iam-policy-strict.json leaves it out on purpose)",
			}
		}
		fix := "attach " + p.policyFile
		if p.name == "organizations" {
			fix = "deploy deploy/costhound-readonly-role.yaml and run from the management account or a delegated administrator"
		}
		return AWSCheckItem{
			Status: awsCheckFIX, Name: p.name,
			Detail: p.action + " denied",
			Fix:    fix,
		}
	}
	return AWSCheckItem{Status: awsCheckNOTE, Name: p.name, Detail: code}
}

func awsCredentialsFix(profile string, _ error) string {
	if profile != "" {
		return fmt.Sprintf("aws sso login --profile %s (or aws configure --profile %s), then costhound -check -profile %s", profile, profile, profile)
	}
	return "aws configure, or aws sso login --profile <name> and add -profile <name>"
}

func appendAWSLicenseNote(rep *AWSCheckReport, plan, licIssue string) {
	if rep == nil || !rep.Ready {
		return
	}
	if license.Rank(plan) >= license.Rank(license.PlanPro) && licIssue == "" {
		return
	}
	if license.Rank(plan) < license.Rank(license.PlanPro) {
		rep.Items = append(rep.Items, AWSCheckItem{
			Status: awsCheckNOTE, Name: "license",
			Detail: "plan free: the free checks run; Pro needs a key",
		})
		return
	}
	if licIssue != "" {
		rep.Items = append(rep.Items, AWSCheckItem{
			Status: awsCheckNOTE, Name: "license",
			Detail: "access is ready; " + licIssue,
		})
	}
}

func awsCheckHasFIX(items []AWSCheckItem) bool {
	for _, it := range items {
		if it.Status == awsCheckFIX {
			return true
		}
	}
	return false
}

func (r *AWSCheckReport) Text(w io.Writer) error {
	if r == nil {
		return nil
	}
	fixes := 0
	for _, it := range r.Items {
		if _, err := fmt.Fprintf(w, "%-4s %s: %s\n", it.Status, it.Name, it.Detail); err != nil {
			return err
		}
		if it.Status == awsCheckFIX {
			fixes++
			if it.Fix != "" {
				if _, err := fmt.Fprintf(w, "     fix: %s\n", it.Fix); err != nil {
					return err
				}
			}
		}
	}
	if fixes == 0 {
		_, err := fmt.Fprintln(w, "Access is ready: run costhound")
		return err
	}
	_, err := fmt.Fprintf(w, "%d thing(s) to fix above, then run costhound -check again\n", fixes)
	return err
}

func (r *AWSCheckReport) JSON(w io.Writer) error {
	if r == nil {
		return errors.New("nil check report")
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func (r *AWSCheckReport) ExitCode() int {
	if r == nil || !r.Ready {
		return 1
	}
	return 0
}

func runAWSCheckCLI(ctx context.Context, cfg aws.Config, lic license.Status, orgID string, asJSON bool, profile, regions string, orgScan bool, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "costhound: checking setup (no findings)")
	opts := AWSCheckOpts{
		Profile: profile,
		Regions: regions,
		OrgScan: orgScan,
		License: lic,
		OrgID:   orgID,
	}
	rep, err := awsCheck(ctx, cfg, lic.Plan, opts)
	if err != nil {
		fmt.Fprintln(stderr, "costhound:", err)
		return 1
	}
	if asJSON {
		err = rep.JSON(stdout)
	} else {
		err = rep.Text(stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "costhound: could not write the checklist")
		return 1
	}
	return rep.ExitCode()
}

// deniedBySCP: AWS says the deny comes from a service control policy, which
// no policy in the account can override (an Organization's SCP, or the
// limits of an account on the AWS Free plan).
func deniedBySCP(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && strings.Contains(strings.ToLower(api.ErrorMessage()), "service control policy")
}
