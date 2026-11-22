package report

import (
	"fmt"
	"io"
)

// AccountResult is one account's line in an Organization report.
type AccountResult struct {
	ID              string  `json:"id"`
	Name            string  `json:"name,omitempty"`
	TotalMonthlyUSD float64 `json:"total_monthly_usd"`
	Findings        int     `json:"findings"`
	Error           string  `json:"error,omitempty"` // why it couldn't be scanned
}

func (r *Report) writeAccounts(w io.Writer) {
	if len(r.Accounts) == 0 {
		return
	}
	fmt.Fprintf(w, "By account:\n")
	for _, a := range r.Accounts {
		name := a.ID
		if a.Name != "" {
			name += " (" + a.Name + ")"
		}
		if a.Error != "" {
			fmt.Fprintf(w, "  %-40s  not scanned: %s\n", name, a.Error)
			continue
		}
		fmt.Fprintf(w, "  %-40s %4d  %12s/mo\n", name, a.Findings, Money(a.TotalMonthlyUSD))
	}
	fmt.Fprintln(w)
}
