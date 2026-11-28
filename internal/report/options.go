package report

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// SetOptions stores cheaper-alternative estimates, biggest saving first.
func (r *Report) SetOptions(os []checks.Option) {
	r.Options = setOptions(r.Account, os)
}

func setOptions(account string, os []checks.Option) []checks.Option {
	var kept []checks.Option
	for _, o := range os {
		if o.SavingMonthlyUSD <= 0 {
			continue
		}
		scope := account
		if scope == "" {
			scope = o.Account
		}
		o.ID = OptionID(scope, o)
		kept = append(kept, o)
	}
	sortOptions(kept)
	return kept
}

// sortOptions: biggest saving first, then account, region and resource.
func sortOptions(os []checks.Option) {
	sort.SliceStable(os, func(i, j int) bool {
		a, b := os[i], os[j]
		if a.SavingMonthlyUSD != b.SavingMonthlyUSD {
			return a.SavingMonthlyUSD > b.SavingMonthlyUSD
		}
		if a.Account+a.Region != b.Account+b.Region {
			return a.Account+a.Region < b.Account+b.Region
		}
		return a.Resource < b.Resource
	})
}

// OptionID is stable across runs for the same resource and alternative kind:
// it hashes the resource's own key (TagRef), not its display label, so
// renaming the resource keeps the ID.
func OptionID(account string, o checks.Option) string {
	res := o.TagRef
	if res == "" {
		res = o.Resource
	}
	key := account + "|" + o.Kind + "|" + o.Region + "|" + res
	sum := sha256.Sum256([]byte(key))
	return "op_" + hex.EncodeToString(sum[:])[:10]
}

func (r *Report) writeOptions(w io.Writer) {
	writeOptions(w, r.Options)
}

func writeOptions(w io.Writer, options []checks.Option) {
	if len(options) == 0 {
		return
	}
	fmt.Fprintln(w, "Cheaper ways to run what you already use (estimates, not in the waste total):")
	const most = 10
	for i, o := range options {
		if i == most {
			fmt.Fprintf(w, "  ... and %d more (-json)\n", len(options)-most)
			break
		}
		where := o.Region
		if o.Account != "" {
			where = o.Account + " " + o.Region
		}
		fmt.Fprintf(w, "  %s %s (%s): about %s/mo now; about %s/mo as a %s\n", o.Current, o.Resource, where, Money(o.CurrentMonthlyUSD), Money(o.AlternativeMonthlyUSD), o.Alternative)
		if o.UpkeepHours > 0 {
			fmt.Fprintf(w, "    saves about %s/mo for about %.0f hours of upkeep a month: worth it if an engineer-hour costs you less than %s\n",
				Money(o.SavingMonthlyUSD), o.UpkeepHours, Money(o.BreakEvenUSDPerHour))
		} else {
			fmt.Fprintf(w, "    saves about %s/mo\n", Money(o.SavingMonthlyUSD))
		}
		for _, t := range o.Tradeoffs {
			fmt.Fprintf(w, "    you give up: %s\n", t)
		}
		for _, a := range o.Assumptions {
			fmt.Fprintf(w, "    assumes: %s\n", a)
		}
		fmt.Fprintln(w)
	}
}
