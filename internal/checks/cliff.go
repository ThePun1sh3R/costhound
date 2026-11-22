package checks

import (
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// Cliff is a dated change in what an account will pay, or in what AWS will
// do to a resource, if nothing changes before then: one line of the
// cost-cliff calendar ("upcoming" in the report, costhound -ics).
type Cliff struct {
	Date       time.Time `json:"date"`
	Kind       string    `json:"kind"` // extended-support-starts, extended-support-step, extended-support-ends, auto-upgrade
	Check      string    `json:"check"`
	Account    string    `json:"account,omitempty"`
	Region     string    `json:"region"`
	Resource   string    `json:"resource"`
	What       string    `json:"what"`
	Outcome    string    `json:"outcome"`     // what happens on that date if nothing changes
	MonthlyUSD float64   `json:"monthly_usd"` // from that date: more (+) or less (-) each month
	Priced     bool      `json:"priced"`
	Source     string    `json:"source"` // where the date comes from: an AWS API, or AWS's docs
	Owner      string    `json:"owner,omitempty"`
	OwnerFrom  string    `json:"owner_from,omitempty"`
	TagRef     string    `json:"-"`
}

// CliffHorizon is how far ahead the calendar looks.
const CliffHorizon = 2 * 365 * 24 * time.Hour

// Cliffs lists the region's coming cost cliffs, by date (paid plans).
func Cliffs(r *inventory.Region, o Options) []Cliff { return cliffs(r, o) }
