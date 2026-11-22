package report

import (
	"sort"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// NoOwner labels the resources that have no owner tag.
const NoOwner = "(no owner tag)"

// OwnerRow is one owner's share: the findings costhound could look up whose
// resource that owner's tag names, or NoOwner.
type OwnerRow struct {
	Owner      string
	Findings   int
	MonthlyUSD float64
	Priced     bool
}

// ByOwner groups the findings costhound could look up by owner, biggest
// first. Owner names match without regard to case ("Payments", "payments").
func ByOwner(fs []checks.Finding) []OwnerRow {
	rows := map[string]*OwnerRow{}
	for _, f := range fs {
		if f.OwnerFrom == "" {
			continue // about several resources, or tags it couldn't read
		}
		name := f.Owner
		if f.Owner == "" {
			name = NoOwner
		}
		k := strings.ToLower(name)
		x, ok := rows[k]
		if !ok {
			x = &OwnerRow{Owner: name}
			rows[k] = x
		}
		x.Findings++
		if f.Priced {
			x.MonthlyUSD += f.MonthlyUSD
			x.Priced = true
		}
	}
	out := make([]OwnerRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MonthlyUSD != out[j].MonthlyUSD {
			return out[i].MonthlyUSD > out[j].MonthlyUSD
		}
		return out[i].Owner < out[j].Owner
	})
	return out
}

// Unowned is the waste on resources that have no owner tag: nobody to ask.
func Unowned(fs []checks.Finding) (n int, usd float64) {
	for _, f := range fs {
		if f.OwnerFrom == "none" {
			n++
			if f.Priced {
				usd += f.MonthlyUSD
			}
		}
	}
	return n, usd
}

// ownerNote is what a finding's line says about its owner (and the
// Terraform resource that manages it).
func ownerNote(f checks.Finding) string {
	note := ""
	switch {
	case f.Owner != "":
		note = "owner " + f.Owner + " (" + f.OwnerFrom + ")"
	case f.OwnerFrom == "none":
		note = "no owner tag"
	}
	if f.TFAddress != "" {
		if note != "" {
			note += " · "
		}
		note += "terraform " + f.TFAddress
	}
	if f.CreatorFrom != "" {
		c := f.CreatorFrom
		if f.Creator != "" {
			c = "created by " + f.Creator + " (" + f.CreatorFrom + ")"
		}
		if note != "" {
			note += " · "
		}
		note += c
	}
	return note
}

// Creators counts what -creators found: unowned findings asked about, those
// with a named creator, and those older than CloudTrail's 90 days.
func Creators(fs []checks.Finding) (asked, named, older int) {
	for _, f := range fs {
		if f.CreatorFrom == "" {
			continue
		}
		asked++
		if f.Creator != "" {
			named++
		}
		if strings.Contains(f.CreatorFrom, "before CloudTrail's 90 days") {
			older++
		}
	}
	return asked, named, older
}
