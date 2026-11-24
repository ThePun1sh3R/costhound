package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// Diff is what changed since the previous saved run of the same account.
// Only regions scanned by both runs are compared, so scanning fewer regions
// never shows up as "fixed".
type Diff struct {
	Since       time.Time        `json:"since"`
	New         []checks.Finding `json:"new"`
	Resolved    []checks.Finding `json:"resolved"`
	NewUSD      float64          `json:"new_monthly_usd"`
	ResolvedUSD float64          `json:"resolved_monthly_usd"`
	Moved       []CliffMove      `json:"moved,omitempty"` // cost cliffs whose date AWS moved since the last run
	newKeys     map[string]bool
}

// findingKey identifies a finding across runs: its stable ID, or for runs
// saved before IDs existed, check|region|resource.
func findingKey(f checks.Finding) string {
	if f.ID != "" {
		return f.ID
	}
	return f.Check + "|" + f.Region + "|" + f.Resource
}

// keyOf is the resource part of a finding's identity: its Key, else its Resource.
func keyOf(f checks.Finding) string {
	if f.Key != "" {
		return f.Key
	}
	return f.Resource
}

// movedKey is how a finding of a check in checks.TookOver (either side) is
// matched to the other side across runs: by the older check's name and the
// resource. A check in checks.Families is matched by its family and the
// resource's ID. Findings with migrated keys use their stable resource identity.
func movedKey(f checks.Finding) string {
	if fam, ok := checks.Families[f.Check]; ok {
		id, _, _ := strings.Cut(keyOf(f), " ") // "vol-… (Name)": the ID, whatever the Name tag says now
		return "family|" + fam + "|" + f.Account + "|" + f.Region + "|" + id
	}
	// cost-hub Storage recs share ResourceID with the instance/cluster rec;
	// Key is Resource+"/storage" so they get distinct finding IDs. Match by
	// Resource across that Key change so an upgrade books no fake fix. An
	// acknowledgement written by finding ID for a storage rec stops matching
	// once.
	if f.Check == "cost-hub" && strings.HasSuffix(f.Facts["resource_type"], "Storage") {
		return "hub-storage|" + f.Account + "|" + f.Region + "|" + f.Resource
	}
	// Older Compute Optimizer reports keyed by short name already stored the
	// ARN and type. Match that identity across the change to ARN-based IDs.
	if k := computeOptimizerKey(f); k != "" {
		return "moved|compute-optimizer|" + f.Account + "|" + f.Region + "|" + k
	}
	old, ok := checks.TookOver[f.Check]
	if !ok {
		for _, o := range checks.TookOver {
			if o == f.Check {
				old, ok = o, true
			}
		}
	}
	if !ok {
		return ""
	}
	return "moved|" + old + "|" + f.Account + "|" + f.Region + "|" + keyOf(f)
}

func computeOptimizerKey(f checks.Finding) string {
	if f.Check != "compute-optimizer" || f.Facts["resource_type"] == "" || f.Facts["arn"] == "" {
		return ""
	}
	return f.Facts["resource_type"] + "|" + f.Facts["arn"]
}

// CliffMove is a cost cliff that moved to another date since the last run.
type CliffMove struct {
	checks.Cliff
	From time.Time `json:"moved_from"`
}

// IsNew reports whether a finding appeared since the previous run.
func (r *Report) IsNew(f checks.Finding) bool {
	if r.Diff == nil {
		return false
	}
	if f.Account != "" && f.ID == "" {
		return r.Diff.newKeys[f.Account+"|"+findingKey(f)]
	}
	return r.Diff.newKeys[findingKey(f)]
}

func (r *Report) writeDiff(w io.Writer) {
	d := r.Diff
	if d == nil {
		return
	}
	fmt.Fprintf(w, "Since the last run (%s): %d new (%s/mo), %d fixed or gone (%s/mo).\n",
		d.Since.UTC().Format("2006-01-02 15:04 UTC"), len(d.New), Money(d.NewUSD), len(d.Resolved), Money(d.ResolvedUSD))
	for i, f := range d.Resolved {
		if i == 5 {
			fmt.Fprintf(w, "  ... and %d more\n", len(d.Resolved)-5)
			break
		}
		fmt.Fprintf(w, "  fixed: %s %s (%s)\n", f.Region, f.Resource, checks.Title(f.Check))
	}
	for _, m := range d.Moved {
		fmt.Fprintf(w, "  date moved: %s %s: %s, now %s (was %s)\n", m.Region, m.Resource, m.What, m.Date.Format("2006-01-02"), m.From.Format("2006-01-02"))
	}
	fmt.Fprintln(w)
}
