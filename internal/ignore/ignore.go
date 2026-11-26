// Package ignore reads .costhound.yaml: findings a team has looked at and
// decided to keep, with a reason, an owner and an expiry date. Acknowledged
// findings leave the total, Slack and the CI gate, but are still listed, and
// an expired acknowledgement brings the finding back.
//
//	ignore:
//	  - id: ch_3f2a9c1b0d                # one finding (costhound explain shows its ID)
//	    reason: kept for the yearly audit
//	    owner: platform-team
//	    until: 2027-03-01
//	  - check: lambda-x86                # a whole check
//	    reason: arm64 migration planned for Q2
//	  - check: unattached-ebs            # a pattern, optionally per account/region
//	    resource: "vol-0abc*"
//	    account: "111111111111"
//	    region: eu-west-1
//	    reason: restore drills
package ignore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

// DefaultFile is looked for in the current directory.
const DefaultFile = ".costhound.yaml"

// Rule is one acknowledgement.
type Rule struct {
	ID       string `yaml:"id"`
	Check    string `yaml:"check"`
	Resource string `yaml:"resource"` // glob (*, ?) on the finding's key or resource
	Account  string `yaml:"account"`
	Region   string `yaml:"region"`
	Reason   string `yaml:"reason"`
	Owner    string `yaml:"owner"`
	Until    string `yaml:"until"` // YYYY-MM-DD, last day it applies
	until    time.Time
}

// File is a parsed .costhound.yaml.
type File struct {
	Ignore []Rule `yaml:"ignore"`
	// OwnerTags are a team's own owner tag keys ("squad"), tried before the
	// defaults (owner, team, created-by, contact).
	OwnerTags []string `yaml:"owner_tags"`
	// Owners merges the spellings of one owner: name -> aliases, matched
	// without regard to case ("payments": [pay-team, payments@example.com]).
	Owners map[string][]string `yaml:"owners"`
	Path   string              `yaml:"-"`
}

// OwnerAliases is the file's owners map; none without a file.
func (f *File) OwnerAliases() map[string][]string {
	if f == nil {
		return nil
	}
	return f.Owners
}

// OwnerKeys is the file's owner_tags; none without a file.
func (f *File) OwnerKeys() []string {
	if f == nil {
		return nil
	}
	return f.OwnerTags
}

// Load reads and checks a rules file. A missing default file is not an
// error (nil, nil); a missing explicit file is.
func Load(p string, explicit bool) (*File, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // a typo ("untill:") must fail loudly, not silently ignore nothing
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	for i := range f.Ignore {
		r := &f.Ignore[i]
		if r.ID == "" && r.Check == "" {
			return nil, fmt.Errorf("%s: rule %d needs an id or a check (a rule matching everything is not allowed)", p, i+1)
		}
		if r.Check != "" && !knownCheck(r.Check) {
			return nil, fmt.Errorf("%s: rule %d: no check named %q", p, i+1, r.Check)
		}
		if r.Resource != "" {
			if _, err := path.Match(r.Resource, ""); err != nil {
				return nil, fmt.Errorf("%s: rule %d: bad resource pattern %q", p, i+1, r.Resource)
			}
		}
		if r.Until != "" {
			t, err := time.Parse("2006-01-02", r.Until)
			if err != nil {
				return nil, fmt.Errorf("%s: rule %d: until must be YYYY-MM-DD, got %q", p, i+1, r.Until)
			}
			r.until = t.AddDate(0, 0, 1) // the whole "until" day still counts
		}
	}
	if len(f.OwnerTags) > 20 {
		return nil, fmt.Errorf("%s: owner_tags lists %d keys; 20 at most", p, len(f.OwnerTags))
	}
	for _, k := range f.OwnerTags {
		if strings.TrimSpace(k) == "" || len(k) > 128 {
			return nil, fmt.Errorf("%s: owner_tags: %q is not a tag key", p, k)
		}
	}
	seen := map[string]string{}
	for name, aliases := range f.Owners {
		if strings.TrimSpace(name) == "" || len(name) > 128 {
			return nil, fmt.Errorf("%s: owners: %q is not an owner name", p, name)
		}
		for _, a := range append([]string{name}, aliases...) {
			k := strings.ToLower(strings.TrimSpace(a))
			if k == "" {
				return nil, fmt.Errorf("%s: owners: %s has an empty alias", p, name)
			}
			if other, ok := seen[k]; ok && other != name {
				return nil, fmt.Errorf("%s: owners: %q is listed under both %s and %s", p, a, other, name)
			}
			seen[k] = name
		}
	}
	f.Path = p
	return &f, nil
}

// matches also lets a rule that names a resource (by ID or by pattern) for an
// older check cover that resource after another check took it over
// (checks.TookOver). A blanket rule for the old check stays with the old
// check: it was written for what that check reported, which isn't
// everything the new one reports (young NATs, free-plan runs).
func (r Rule) matches(f checks.Finding, account string) bool {
	if r.matchesOne(f, account) {
		return true
	}
	if old, ok := checks.TookOver[f.Check]; ok && (r.ID != "" || r.Resource != "") {
		twin := f
		twin.Check = old
		twin.ID = report.FindingID(account, twin)
		return r.matchesOne(twin, account)
	}
	return false
}

func (r Rule) matchesOne(f checks.Finding, account string) bool {
	if r.ID != "" && r.ID != f.ID {
		return false
	}
	if r.Check != "" && r.Check != f.Check {
		return false
	}
	if r.Account != "" && r.Account != account {
		return false
	}
	if r.Region != "" && r.Region != f.Region {
		return false
	}
	if r.Resource != "" {
		k, _ := path.Match(r.Resource, f.Key)
		res, _ := path.Match(r.Resource, f.Resource)
		if !k && !res {
			return false
		}
	}
	return true
}

func (r Rule) summary() report.AckRule {
	return report.AckRule{ID: r.ID, Check: r.Check, Resource: r.Resource, Account: r.Account, Region: r.Region, Reason: r.Reason, Owner: r.Owner, Until: r.Until}
}

// extraChecks holds check IDs registered outside checks.All (Azure/GCP on
// pro builds). RegisterCheck is safe to call from init.
var extraChecks = map[string]bool{}

func knownCheck(id string) bool {
	if extraChecks[id] {
		return true
	}
	for _, c := range checks.All {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Apply moves acknowledged findings out of the report's findings (and its
// total) into Acknowledged, and notes expired and unused rules.
func (f *File) Apply(rep *report.Report, now time.Time) {
	if f == nil {
		return
	}
	kept, total, acked, expired, unused := f.apply(rep.Findings, now, func(fd checks.Finding) string {
		if fd.Account != "" {
			return fd.Account
		}
		return rep.Account
	})
	rep.Findings, rep.TotalMonthlyUSD = kept, total
	rep.Acknowledged = append(rep.Acknowledged, acked...)
	rep.ExpiredAcks = append(rep.ExpiredAcks, expired...)
	rep.UnusedRules = append(rep.UnusedRules, unused...)
	rep.IgnoreFile = f.Path
}

func (f *File) apply(findings []checks.Finding, now time.Time, accountOf func(checks.Finding) string) (kept []checks.Finding, total float64, acked []report.Acknowledged, expired, unused []report.AckRule) {
	used := make([]bool, len(f.Ignore))
	expiredSeen := map[int]bool{}
	for _, fd := range findings {
		wasAcked := false
		for i, r := range f.Ignore {
			if !r.matches(fd, accountOf(fd)) {
				continue
			}
			used[i] = true
			if !r.until.IsZero() && !now.Before(r.until) {
				if !expiredSeen[i] {
					expiredSeen[i] = true
					expired = append(expired, r.summary())
				}
				continue // expired: the finding comes back
			}
			acked = append(acked, report.Acknowledged{Finding: fd, Reason: r.Reason, Owner: r.Owner, Until: r.Until})
			wasAcked = true
			break
		}
		if !wasAcked {
			kept = append(kept, fd)
			if fd.Priced {
				total += fd.MonthlyUSD
			}
		}
	}
	for i, u := range used {
		if !u {
			unused = append(unused, f.Ignore[i].summary())
		}
	}
	if kept == nil {
		kept = []checks.Finding{}
	}
	return kept, total, acked, expired, unused
}
