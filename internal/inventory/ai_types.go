package inventory

import "time"

const NotebookReviewDays = 30

// AIRecentMetricDays is the lookback of CloudWatch ListMetrics.
const AIRecentMetricDays = 14

// AIUsage holds the inventory and complete usage samples for the Pro AI checks.
type AIUsage struct {
	Window         int
	Throughputs    []BedrockThroughput
	Notebooks      []SageMakerNotebook
	Indexes        []KendraIndex
	Invocations    map[string]float64 // provisioned model ARN
	Queries        map[string]float64 // index ID
	NoRecentSeries map[string]bool    // ARN or index ID with no recently listed activity series
}

type BedrockThroughput struct {
	ARN, Name, Status, ModelARN, FoundationARN, Commitment string
	Created, CommitmentEnd                                 *time.Time
	Units                                                  int32
	Rate                                                   AIPrice
}

type SageMakerNotebook struct {
	ARN, Name, Status, InstanceType string
	Modified                        *time.Time
	VolumeGB                        int32
	HasLifecycle                    bool
	Compute, Storage                AIPrice
}

type KendraIndex struct {
	ID, Name, Status, Edition string
	Created                   *time.Time
	QueryUnits, StorageUnits  int32   // additional units, above the included base capacity
	Rate                      AIPrice // combined hourly rate
}

// AIPrice is a list price, with its fallback provenance for the finding.
type AIPrice struct {
	USD     float64
	BuiltIn bool
	Reason  string // why the list price is unavailable
}
