package checks

import (
	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// Option is a cheaper way to run something the customer already uses: both
// sides of the math from measured usage. These are estimates only — never part
// of the waste total, diff, ledger, summary, or CI gates.
type Option struct {
	ID                    string   `json:"id"` // op_ + stable hash (like finding IDs)
	Kind                  string   `json:"kind"`
	Account               string   `json:"account,omitempty"`
	Region                string   `json:"region"`
	Resource              string   `json:"resource"`
	Current               string   `json:"current"`
	CurrentMonthlyUSD     float64  `json:"current_monthly_usd"`
	Alternative           string   `json:"alternative"`
	AlternativeMonthlyUSD float64  `json:"alternative_monthly_usd"`
	SavingMonthlyUSD      float64  `json:"saving_monthly_usd"`
	UpkeepHours           float64  `json:"upkeep_hours_per_month"`
	BreakEvenUSDPerHour   float64  `json:"break_even_usd_per_hour"`
	Tradeoffs             []string `json:"tradeoffs"`
	Assumptions           []string `json:"assumptions"`
	Evidence              []string `json:"evidence,omitempty"`
	Confidence            string   `json:"confidence"` // low, medium or high
	Owner                 string   `json:"owner,omitempty"`
	OwnerFrom             string   `json:"owner_from,omitempty"`
	TagRef                string   `json:"-"`
}

// RegionOptions lists cheaper alternatives for the region (paid plans).
func RegionOptions(r *inventory.Region, o Options) []Option { return regionOptions(r, o) }
