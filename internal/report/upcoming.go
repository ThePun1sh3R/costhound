package report

import (
	"fmt"
	"io"
	"sort"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// SetUpcoming stores the cost-cliff calendar, earliest first.
func (r *Report) SetUpcoming(cs []checks.Cliff) {
	r.Upcoming = append([]checks.Cliff(nil), cs...)
	sortCliffs(r.Upcoming)
}

func sortCliffs(cs []checks.Cliff) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		if a.Account+a.Region != b.Account+b.Region {
			return a.Account+a.Region < b.Account+b.Region
		}
		return a.Resource < b.Resource
	})
}

// signed is a monthly change: +$87.60 or -$365.00.
func signed(v float64) string {
	if v < 0 {
		return "-" + Money(-v)
	}
	return "+" + Money(v)
}

// MonthSteps is the monthly bill change from the calendar, as a running
// total at the end of each month that has a priced cliff.
func MonthSteps(cs []checks.Cliff) (months []string, total []float64) {
	var run float64
	for _, c := range cs {
		if !c.Priced {
			continue
		}
		run += c.MonthlyUSD
		m := c.Date.Format("2006-01")
		if n := len(months); n > 0 && months[n-1] == m {
			total[n-1] = run
			continue
		}
		months, total = append(months, m), append(total, run)
	}
	return months, total
}

func (r *Report) writeUpcoming(w io.Writer) {
	if len(r.Upcoming) == 0 {
		return
	}
	fmt.Fprintf(w, "Coming up, if nothing changes (the next %d months):\n", int(checks.CliffHorizon.Hours()/24/30.4))
	const most = 12
	for i, c := range r.Upcoming {
		if i == most {
			fmt.Fprintf(w, "  ... and %d more (-json, -ics)\n", len(r.Upcoming)-most)
			break
		}
		who := c.Region
		if c.Account != "" {
			who = c.Account + " " + c.Region
		}
		amount := ""
		if c.Priced && c.MonthlyUSD != 0 {
			amount = fmt.Sprintf(" (%s/mo)", signed(c.MonthlyUSD))
		}
		fmt.Fprintf(w, "  %s  %-14s %s: %s\n", c.Date.Format("2006-01-02"), who, c.Resource, c.What)
		fmt.Fprintf(w, "              %s%s\n", c.Outcome, amount)
	}
	if months, total := MonthSteps(r.Upcoming); len(months) > 0 {
		fmt.Fprintf(w, "  Monthly bill change, running total:")
		for i := range months {
			if i == 8 {
				fmt.Fprintf(w, " ...")
				break
			}
			fmt.Fprintf(w, " %s %s", months[i], signed(total[i]))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}
