package report

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// Hidden is what -min-usd left out of a report: the priced findings under
// it, counted and summed by check, so a report never hides that it hid
// something. Keys name them for the diff (each one's ID, and its movedKey
// when it has one): a hidden finding is still there, so it's neither fixed
// nor new. Keys are left out of redacted copies (a movedKey holds the
// resource's ID).
type Hidden struct {
	MinUSD     float64       `json:"min_usd"`
	Count      int           `json:"count"`
	MonthlyUSD float64       `json:"monthly_usd"`
	ByCheck    []HiddenCheck `json:"by_check"`
	Keys       []string      `json:"keys,omitempty"`
}

// HiddenCheck is one check's part of Hidden.
type HiddenCheck struct {
	Check      string  `json:"check"`
	Count      int     `json:"count"`
	MonthlyUSD float64 `json:"monthly_usd"`
}

func (h *Hidden) add(f checks.Finding) {
	h.Keys = append(h.Keys, findingKey(f))
	if m := movedKey(f); m != "" {
		h.Keys = append(h.Keys, m)
	}
	h.addCheck(HiddenCheck{Check: f.Check, Count: 1, MonthlyUSD: f.MonthlyUSD})
}

func (h *Hidden) addCheck(c HiddenCheck) {
	h.Count += c.Count
	h.MonthlyUSD += c.MonthlyUSD
	for i := range h.ByCheck {
		if h.ByCheck[i].Check == c.Check {
			h.ByCheck[i].Count += c.Count
			h.ByCheck[i].MonthlyUSD += c.MonthlyUSD
			return
		}
	}
	h.ByCheck = append(h.ByCheck, c)
}

// done sorts the checks biggest first and the keys, once all are added.
func (h *Hidden) done() {
	slices.Sort(h.Keys)
	h.Keys = slices.Compact(h.Keys)
	sort.SliceStable(h.ByCheck, func(i, j int) bool {
		a, b := h.ByCheck[i], h.ByCheck[j]
		if a.MonthlyUSD != b.MonthlyUSD {
			return a.MonthlyUSD > b.MonthlyUSD
		}
		return a.Check < b.Check
	})
}

// Line says what -min-usd hid, by check, and how to see it ("" when it hid
// nothing).
func (h *Hidden) Line() string {
	if h == nil || h.Count == 0 {
		return ""
	}
	var parts []string
	for i, c := range h.ByCheck {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("%d more check(s)", len(h.ByCheck)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d, %s", c.Check, c.Count, Money(c.MonthlyUSD)))
	}
	return fmt.Sprintf("Hidden: %d finding(s) under %s/month each, %s/month together (%s): -min-usd 0 shows them",
		h.Count, Money(h.MinUSD), Money(h.MonthlyUSD), strings.Join(parts, "; "))
}

// Nothing is the line for a report with no finding shown.
func (h *Hidden) Nothing() string {
	if h == nil || h.Count == 0 {
		return "No waste found by these checks."
	}
	return fmt.Sprintf("Nothing at or above %s/month by these checks.", Money(h.MinUSD))
}

// smallEach are the checks whose findings are small by nature (one alarm,
// one secret, one hosted zone): -min-usd judges them by what the check's
// findings in a region cost together, so 200 orphan alarms at $0.10 show as
// the $20 a month they are, and one alone stays hidden.
var smallEach = map[string]bool{"orphan-alarm": true, "unused-secret": true, "empty-hosted-zone": true, "ec2-detailed-monitoring": true}

// underMin says which priced findings -min-usd hides: each one on its own,
// or, for a smallEach check, its account's region's total for that check.
func underMin(findings []checks.Finding, minUSD float64) func(checks.Finding) bool {
	group := map[string]float64{}
	key := func(f checks.Finding) string { return f.Account + "|" + f.Check + "|" + f.Region }
	for _, f := range findings {
		if f.Priced && smallEach[f.Check] {
			group[key(f)] += f.MonthlyUSD
		}
	}
	return func(f checks.Finding) bool {
		if !f.Priced {
			return false
		}
		if smallEach[f.Check] {
			return group[key(f)] < minUSD
		}
		return f.MonthlyUSD < minUSD
	}
}
