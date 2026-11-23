package report

import (
	"fmt"
	"io"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/runlog"
)

// RunInfo is what one run did in AWS: every call it made (costhound refuses
// writes and secret reads before they are sent), as whom, and when. Its user
// agent names the run, so the account's own CloudTrail can confirm it.
type RunInfo struct {
	ID        string        `json:"id"`
	UserAgent string        `json:"user_agent"`
	Caller    string        `json:"caller,omitempty"`
	Started   time.Time     `json:"started"`
	Finished  time.Time     `json:"finished"`
	Calls     []runlog.Call `json:"calls"`
}

// RedactInfo marks a copy made for sharing outside the account (-redact).
type RedactInfo struct {
	// Commitment is the SHA-256 of the full report's JSON bytes as written by -redact-full.
	Commitment string `json:"commitment"`
	Note       string `json:"note"`
}

// Totals counts the calls by outcome.
func (ri *RunInfo) Totals() (all, denied, refused int) {
	for _, c := range ri.Calls {
		all += c.Count
		switch c.Outcome {
		case "denied":
			denied += c.Count
		case "refused":
			refused += c.Count
		}
	}
	return all, denied, refused
}

// CloudTrailCheck is a command that lists this run's write events in one
// region: it should print [] (CloudTrail keeps 90 days of event history).
func (ri *RunInfo) CloudTrailCheck(region string) string {
	return fmt.Sprintf("aws cloudtrail lookup-events --region %s --start-time %s --end-time %s "+
		"--lookup-attributes AttributeKey=ReadOnly,AttributeValue=false --query \"Events[?contains(CloudTrailEvent, '%s')]\"",
		region, ri.Started.Add(-time.Minute).UTC().Format(time.RFC3339), ri.Finished.Add(time.Minute).UTC().Format(time.RFC3339), ri.UserAgent)
}

func (r *Report) writeRun(w io.Writer) {
	ri := r.Run
	if ri == nil {
		return
	}
	all, denied, refused := ri.Totals()
	fmt.Fprintf(w, "This run (%s): %d read-only AWS calls", ri.ID, all-refused)
	if denied > 0 {
		fmt.Fprintf(w, ", %d of them denied", denied)
	}
	fmt.Fprintln(w, ". costhound refuses any call that could change something, or that returns secret values or data.")
	if refused > 0 {
		fmt.Fprintf(w, "  %d call(s) refused before they were sent: see \"run\" in -json.\n", refused)
	}
	if len(r.Regions) > 0 {
		fmt.Fprintf(w, "  Check it in CloudTrail, region by region (expect []):\n  %s\n", ri.CloudTrailCheck(r.Regions[0]))
	}
	fmt.Fprintln(w)
}
