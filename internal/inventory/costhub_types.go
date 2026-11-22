package inventory

import "github.com/aws/aws-sdk-go-v2/service/costoptimizationhub"

// CostHubAPI is the AWS Cost Optimization Hub call (us-east-1 endpoint).
type CostHubAPI interface {
	costoptimizationhub.ListRecommendationsAPIClient
}

// Recommendation is one Cost Optimization Hub recommendation. Its savings are
// AWS's own estimate at the account's real prices (discounts included).
type Recommendation struct {
	ID, Region, ResourceID, ResourceType, Action string
	Current, Recommended                         string // resource summaries, e.g. "m5.2xlarge" -> "m7g.xlarge"
	MonthlySavings, MonthlyCost                  float64
	Effort, Source                               string
	Restart, Rollback                            bool
	LookbackDays                                 int
	ResourceARN                                  string // the resource's ARN when it has one
}

// CostHub is what Cost Optimization Hub said for the account.
type CostHub struct {
	Enrolled bool
	Recs     []Recommendation
}
