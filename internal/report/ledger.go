package report

import (
	"fmt"
	"io"
	"time"
)

// LedgerEntry is one fix costhound verified: the finding disappeared in a
// complete scan with the same thresholds. If it comes back, BackAt is set
// and it stops counting from then.
type LedgerEntry struct {
	ID         string     `json:"id"`
	Check      string     `json:"check"`
	Region     string     `json:"region"`
	Resource   string     `json:"resource"`
	MonthlyUSD float64    `json:"monthly_usd"`
	FixedAt    time.Time  `json:"fixed_at"`
	BackAt     *time.Time `json:"back_at,omitempty"`
	Ends       *time.Time `json:"ends,omitempty"` // when it would have stopped costing anyway (the finding's "ends" fact)
}

// Ledger sums the verified fixes of an account.
type Ledger struct {
	Fixes         int       `json:"fixes"`           // still fixed
	CameBack      int       `json:"came_back"`       // fixed once, back since
	MonthlyNow    float64   `json:"monthly_usd_now"` // spend that no longer happens each month
	SavedToDate   float64   `json:"saved_usd_to_date"`
	Since         time.Time `json:"since,omitempty"`
	ThisRunFixes  int       `json:"this_run_fixes"`
	ThisRunUSD    float64   `json:"this_run_monthly_usd"`
	Unverified    bool      `json:"unverified,omitempty"` // thresholds changed: this run's fixes weren't counted
	UnverifiedWhy string    `json:"unverified_why,omitempty"`
}

const hoursPerMonth = 730.0

func (r *Report) writeLedger(w io.Writer) {
	l := r.Ledger
	if l == nil || (l.Fixes == 0 && l.CameBack == 0 && !l.Unverified) {
		return
	}
	fmt.Fprintf(w, "Verified savings: %d fix(es) since %s, %s/mo no longer spent, %s saved so far",
		l.Fixes, l.Since.UTC().Format("2006-01-02"), Money(l.MonthlyNow), Money(l.SavedToDate))
	if l.ThisRunFixes > 0 {
		fmt.Fprintf(w, " (+%d this run, %s/mo)", l.ThisRunFixes, Money(l.ThisRunUSD))
	}
	fmt.Fprintln(w, ".")
	if l.CameBack > 0 {
		fmt.Fprintf(w, "  %d earlier fix(es) came back and no longer count.\n", l.CameBack)
	}
	if l.Unverified {
		fmt.Fprintf(w, "  Not counted this run: %s.\n", l.UnverifiedWhy)
	}
	fmt.Fprintln(w)
}
