package inventory

import "time"

// GlueDevEndpointIdleDays: a READY development endpoint whose last change is
// at least this old is a low-confidence idle candidate (no usage metric).
const GlueDevEndpointIdleDays = 7

// GlueSessionReviewMinutes: a READY interactive session younger than this is
// not judged "idle for a long time" yet.
const GlueSessionReviewMinutes = 30

// GlueUsage holds Glue interactive sessions and legacy development endpoints.
type GlueUsage struct {
	Sessions     []GlueSession
	DevEndpoints []GlueDevEndpoint
}

// GlueSession is an interactive session from ListSessions / GetSession.
type GlueSession struct {
	ARN, ID, Status, WorkerType, GlueVersion string
	Created                                  *time.Time
	IdleTimeoutMin                           int32 // minutes; 0 if AWS omitted it
	DPUs                                     float64
	Rate                                     GluePrice // $/DPU-hour
}

// GlueDevEndpoint is a legacy Glue development endpoint.
type GlueDevEndpoint struct {
	ARN, Name, Status, WorkerType string
	Created, Modified             *time.Time
	DPUs                          float64
	Rate                          GluePrice
}

// GluePrice is a $/DPU-hour list rate with provenance for the finding.
type GluePrice struct {
	USD     float64
	BuiltIn bool
	Reason  string
}
