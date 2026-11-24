// Package report renders a scan as terminal text or JSON.
package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

// PriceNote is printed with every report.
const PriceNote = "Prices are approximate us-east-1 list prices; most other regions cost the same or up to ~20% more."

// SafetyNote is printed with every text report.
const SafetyNote = "costhound only reads your account and never changes anything. The fix commands are for you to review and run."

// maxFixLines caps the fix commands shown per finding in text output.
const maxFixLines = 3

// Report is one scan.
type Report struct {
	Tool            string           `json:"tool"`
	Version         string           `json:"version"`
	Plan            string           `json:"plan"` // "free" or "pro"
	Account         string           `json:"account"`
	Org             string           `json:"org,omitempty"`     // the AWS Organization, when known
	OrgHub          bool             `json:"org_hub,omitempty"` // this run read the Organization's Cost Optimization Hub
	ScannedAt       time.Time        `json:"scanned_at"`
	Regions         []string         `json:"regions"`
	TotalMonthlyUSD float64          `json:"total_monthly_usd"`
	Findings        []checks.Finding `json:"findings"`
	Skipped         []inventory.Skip `json:"skipped"`
	BlockedRegions  []Blocked        `json:"blocked_regions"`
	NotOffered      []NotOffered     `json:"not_offered"`        // services with no endpoint in a reached region
	Diff            *Diff            `json:"diff,omitempty"`     // paid plans: change since the last saved run
	Accounts        []AccountResult  `json:"accounts,omitempty"` // Organization scans: one line per account
	Settings        string           `json:"settings,omitempty"` // the thresholds of this run, to tell real fixes from changed settings
	// Stopped: the instances stopped (or stopping) at this run, as
	// "<account>|<region>|<id>" (account empty in a one-account report). The
	// diff uses it to tell a disk left alone while its instance is stopped
	// from a fixed one. Left out of redacted copies.
	Stopped []string `json:"stopped_instances,omitempty"`
	// EKSClusters: every EKS cluster this run listed, as
	// "<account>|<region>|<name>" (account empty in a one-account report). The
	// diff uses it to keep a Karpenter finding while its cluster exists.
	// Left out of redacted copies.
	EKSClusters []string `json:"eks_clusters,omitempty"`
	// Parked: findings of earlier runs whose resource still exists but a
	// check couldn't judge this run (an Auto Scaling group scaled to zero), as
	// "<account>|<region>|<check>|<key>". Neither fixed nor new, and not in
	// the totals; only resources that once had a finding (KeepParked). Saved,
	// so a diff of two saved runs (the dashboard) agrees with the live one.
	// Left out of redacted copies.
	Parked []string `json:"parked,omitempty"`
	// OpenSearchDataNodes records the observed data-node configuration under
	// the same key as Parked. A listed but transitional domain has no size.
	OpenSearchDataNodes map[string]inventory.NodeGroup `json:"opensearch_data_nodes,omitempty"`
	// PendingOpenSearchResizes keep a previously reported resize verifiable
	// when load or deployment temporarily takes it out of the findings.
	PendingOpenSearchResizes []checks.Finding `json:"pending_opensearch_resizes,omitempty"`
	// ParkedRedshiftResizes carries the prior sizing recommendation through
	// scans where its cluster is still at a different size or is mid-resize.
	ParkedRedshiftResizes []checks.Finding `json:"parked_redshift_resizes,omitempty"`
	// RedshiftClusters is this scan's list by ARN, used to verify a resize.
	RedshiftClusters map[string]inventory.Warehouse `json:"-"`
	// Unjudged: this run's "|<region>|<check>|<key>" of every resource a
	// check couldn't judge, the raw list KeepParked bounds. Not saved.
	Unjudged     []string       `json:"-"`
	Ledger       *Ledger        `json:"ledger,omitempty"` // paid plans: verified savings so far
	Hidden       *Hidden        `json:"hidden,omitempty"` // what -min-usd left out, by check
	IgnoreFile   string         `json:"ignore_file,omitempty"`
	Acknowledged []Acknowledged `json:"acknowledged,omitempty"` // in .costhound.yaml: listed, not in the total
	ExpiredAcks  []AckRule      `json:"expired_acknowledgements,omitempty"`
	UnusedRules  []AckRule      `json:"unused_rules,omitempty"`
	PriceNote    string         `json:"price_note"`
	Run          *RunInfo       `json:"run,omitempty"`       // what this run called, to check against CloudTrail
	Redaction    *RedactInfo    `json:"redaction,omitempty"` // set on a copy made for sharing (-redact)
	Upcoming     []checks.Cliff `json:"upcoming,omitempty"`  // paid plans: the cost-cliff calendar, by date
	TFStates     []string       `json:"tf_states,omitempty"` // Terraform state files read (-tf-state)
}

// New builds a report, dropping priced findings under minUSD (counted in
// Hidden) and sorting the rest biggest first (unpriced last).
func New(version, account string, at time.Time, regions []string, findings []checks.Finding, skipped []inventory.Skip, minUSD float64) *Report {
	var kept []checks.Finding
	var total float64
	var hidden *Hidden
	for i := range findings {
		findings[i].ID = FindingID(account, findings[i])
	}
	under := underMin(findings, minUSD)
	for _, f := range findings {
		if under(f) {
			if hidden == nil {
				hidden = &Hidden{MinUSD: minUSD}
			}
			hidden.add(f)
			continue
		}
		kept = append(kept, f)
		if f.Priced {
			total += f.MonthlyUSD
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.Priced != b.Priced {
			return a.Priced
		}
		if a.MonthlyUSD != b.MonthlyUSD {
			return a.MonthlyUSD > b.MonthlyUSD
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.Resource < b.Resource
	})
	sort.Strings(regions)
	sort.SliceStable(skipped, func(i, j int) bool {
		if skipped[i].Region != skipped[j].Region {
			return skipped[i].Region < skipped[j].Region
		}
		return skipped[i].What < skipped[j].What
	})
	if kept == nil {
		kept = []checks.Finding{}
	}
	skipped, blocked, notOffered := splitSkips(regions, skipped)
	if skipped == nil {
		skipped = []inventory.Skip{}
	}
	if blocked == nil {
		blocked = []Blocked{}
	}
	if notOffered == nil {
		notOffered = []NotOffered{}
	}
	if hidden != nil {
		hidden.done()
	}
	return &Report{
		Tool: "costhound", Version: version, Plan: "free", Account: account, ScannedAt: at.UTC(),
		Regions: regions, TotalMonthlyUSD: total, Findings: kept, Skipped: skipped, BlockedRegions: blocked,
		NotOffered: notOffered, PriceNote: PriceNote, Hidden: hidden,
	}
}

// JSON writes the report as indented JSON.
func (r *Report) JSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Text writes the human report.
func (r *Report) Text(w io.Writer, showFix bool) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("costhound %s (%s) · AWS waste report\n", r.Version, r.Plan)
	if len(r.Accounts) > 0 {
		p("Organization %s · %d account(s) · %d region(s) · %s\n\n", r.Account, len(r.Accounts), len(r.Regions), r.ScannedAt.Format("2006-01-02 15:04 UTC"))
	} else {
		p("Account %s · %d region(s) · %s\n\n", r.Account, len(r.Regions), r.ScannedAt.Format("2006-01-02 15:04 UTC"))
	}

	if r.Redaction != nil {
		p("Redacted copy: %s\nSHA-256 of the full report (the file -redact-full saves): %s\n\n", r.Redaction.Note, r.Redaction.Commitment)
	}
	if len(r.Findings) == 0 {
		if line := r.Hidden.Line(); line != "" {
			p("%s\n%s\n\n", r.Hidden.Nothing(), line)
		} else {
			p("No waste found by these checks.\n\n")
		}
	} else {
		p("Estimated waste: %s / month  (about %s / year)\n", Money(r.TotalMonthlyUSD), Money(r.TotalMonthlyUSD*12))
		if len(r.TFStates) > 0 {
			var in, out float64
			for _, f := range r.Findings {
				if f.TFAddress != "" {
					in += f.MonthlyUSD
				} else {
					out += f.MonthlyUSD
				}
			}
			p("In Terraform: %s / month (fix it in code); in none of the %d state file(s) given: %s / month\n", Money(in), len(r.TFStates), Money(out))
		}
		if asked, named, older := Creators(r.Findings); asked > 0 {
			p("Creators (CloudTrail, -creators): asked about %d unowned finding(s): %d named, %d older than its 90 days\n", asked, named, older)
		}
		if n, usd := Unowned(r.Findings); n > 0 {
			p("Unowned: %s / month of it on %d resource(s) with no owner tag: nobody to ask (see By owner)\n", Money(usd), n)
		}
		if line := r.Hidden.Line(); line != "" {
			p("%s\n", line)
		}
		p("\n")
		r.summary(w)
	}
	r.writeUpcoming(w)
	r.writeAccounts(w)
	r.writeDiff(w)
	r.writeLedger(w)

	var priced, unpriced []checks.Finding
	for _, f := range r.Findings {
		if f.Priced {
			priced = append(priced, f)
		} else {
			unpriced = append(unpriced, f)
		}
	}

	if len(priced) > 0 {
		p("Findings, biggest first:\n\n")
		for _, f := range priced {
			p("  %10s/mo  %s%-14s %s%s\n", Money(f.MonthlyUSD), acct(f), f.Region, f.Resource, r.newMark(f))
			p("      %s: %s\n", checks.Title(f.Check), f.Detail)
			if note := ownerNote(f); note != "" {
				p("      %s · confidence %s · %s\n", f.ID, f.Confidence, note)
			} else {
				p("      %s · confidence %s\n", f.ID, f.Confidence)
			}
			if showFix {
				writeFix(w, f.Fix)
			}
			p("\n")
		}
	}

	if len(unpriced) > 0 {
		p("Not priced, worth a look:\n\n")
		for _, f := range unpriced {
			p("  %s%-14s %s%s\n", acct(f), f.Region, f.Resource, r.newMark(f))
			p("      %s: %s\n", checks.Title(f.Check), f.Detail)
			if note := ownerNote(f); note != "" {
				p("      %s\n", note)
			}
			if showFix {
				writeFix(w, f.Fix)
			}
			p("\n")
		}
	}

	r.writeAcks(w)
	if len(r.Skipped) > 0 {
		writeSkips(w, r.Regions, r.Skipped)
	}
	if len(r.BlockedRegions) > 0 {
		writeBlocked(w, r.BlockedRegions)
	}

	r.writeRun(w)
	if r.Plan == "free" {
		if n := checks.Count(license.PlanPro); n > 0 {
			p("Pro adds %d more checks (usage-based idle resources, AWS Compute Optimizer and Cost Optimization Hub advice, extended-support cliffs, ...): %s\n", n, license.RenewURL)
		}
		if checks.Count(license.PlanTeam) > 0 {
			p("Team adds Organization-wide reports, Slack and CI gates.\n")
		}
		p("costhound Pro also writes each fix as a script to review and run, with a backup where AWS allows one, and a rollback (costhound plan).\n\n")
	} else if len(r.Findings) > 0 {
		p("Fix scripts to review and run, with a backup where AWS allows one, and a rollback: costhound plan -out <new folder>\n\n")
	}

	note := r.PriceNote
	if note == "" {
		note = PriceNote
	}
	p("%s\n%s\n", note, SafetyNote)
}

func (r *Report) summary(w io.Writer) {
	type row struct {
		n      int
		usd    float64
		priced bool
	}
	rows := map[string]*row{}
	var order []string
	for _, c := range checks.All {
		order = append(order, c.ID)
	}
	for _, f := range r.Findings {
		x, ok := rows[f.Check]
		if !ok {
			x = &row{}
			rows[f.Check] = x
		}
		x.n++
		x.usd += f.MonthlyUSD
		x.priced = x.priced || f.Priced
	}
	width := 46 // the title column grows to the longest title shown
	for id := range rows {
		width = max(width, len(checks.Title(id)))
	}
	fmt.Fprintf(w, "By check:\n")
	for _, id := range order {
		x, ok := rows[id]
		if !ok {
			continue
		}
		amount := "not priced"
		if x.priced {
			amount = Money(x.usd) + "/mo"
		}
		fmt.Fprintf(w, "  %-*s %4d  %14s\n", width, checks.Title(id), x.n, amount)
	}
	fmt.Fprintln(w)
	if owners := ByOwner(r.Findings); len(owners) > 0 {
		fmt.Fprintf(w, "By owner:\n")
		const most = 15
		for i, o := range owners {
			if i == most {
				var n int
				var usd float64
				for _, x := range owners[most:] {
					n += x.Findings
					usd += x.MonthlyUSD
				}
				fmt.Fprintf(w, "  %-46s %4d  %14s\n", fmt.Sprintf("%d more owner(s)", len(owners)-most), n, Money(usd)+"/mo")
				break
			}
			amount := "not priced"
			if o.Priced {
				amount = Money(o.MonthlyUSD) + "/mo"
			}
			fmt.Fprintf(w, "  %-46s %4d  %14s\n", o.Owner, o.Findings, amount)
		}
		fmt.Fprintln(w)
	}
}

func writeFix(w io.Writer, fix []string) {
	for i, line := range fix {
		if i == maxFixLines {
			fmt.Fprintf(w, "      ... and %d more (run with -json for all of them)\n", len(fix)-maxFixLines)
			break
		}
		prefix := "      fix: "
		if i > 0 {
			prefix = "           "
		}
		fmt.Fprintf(w, "%s%s\n", prefix, line)
	}
}

// Money formats dollars with thousands separators: 1234.5 -> "$1,234.50".
func Money(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%.2f", v)
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := "$" + b.String() + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

func (r *Report) newMark(f checks.Finding) string {
	if r.IsNew(f) {
		return "  [new]"
	}
	return ""
}

// acct prefixes a finding's account in Organization reports.
func acct(f checks.Finding) string {
	if f.Account == "" {
		return ""
	}
	return f.Account + "  "
}

// FindingID is a finding's stable ID: the same resource with the same problem
// gets the same ID in every run, so it can be acknowledged in .costhound.yaml.
func FindingID(account string, f checks.Finding) string {
	key := account + "|" + f.Check + "|" + f.Region + "|" + keyOf(f)
	switch f.Check {
	case "opensearch-oversized":
		// Each successive downsize is a different repair on the same domain.
		key += "|" + f.Facts["instance_type"] + "|" + f.Facts["nodes"]
	case "redshift-oversized":
		// The same for a cluster: its node type and count when reported.
		key += "|" + f.Facts["node_type"] + "|" + f.Facts["nodes"]
	}
	sum := sha256.Sum256([]byte(key))
	return "ch_" + hex.EncodeToString(sum[:])[:10]
}

// Acknowledged is a finding a team decided to keep, with why.
type Acknowledged struct {
	checks.Finding
	Reason string `json:"ack_reason,omitempty"`
	Owner  string `json:"ack_owner,omitempty"`
	Until  string `json:"ack_until,omitempty"`
}

// AckRule describes a .costhound.yaml rule in the report.
type AckRule struct {
	ID       string `json:"id,omitempty"`
	Check    string `json:"check,omitempty"`
	Resource string `json:"resource,omitempty"`
	Account  string `json:"account,omitempty"`
	Region   string `json:"region,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Until    string `json:"until,omitempty"`
}

func (a AckRule) String() string {
	what := a.ID
	if what == "" {
		what = a.Check
		if a.Resource != "" {
			what += " " + a.Resource
		}
	}
	if a.Until != "" {
		what += " (until " + a.Until + ")"
	}
	return what
}

func (r *Report) writeAcks(w io.Writer) {
	if len(r.Acknowledged) > 0 {
		sum := 0.0
		for _, a := range r.Acknowledged {
			if a.Priced {
				sum += a.MonthlyUSD
			}
		}
		fmt.Fprintf(w, "Acknowledged in %s, not in the total: %d finding(s), %s/mo\n", r.IgnoreFile, len(r.Acknowledged), Money(sum))
		for i, a := range r.Acknowledged {
			if i == 5 {
				fmt.Fprintf(w, "  ... and %d more (-json lists all)\n", len(r.Acknowledged)-5)
				break
			}
			who := ""
			if a.Owner != "" {
				who = ", " + a.Owner
			}
			if a.Until != "" {
				who += ", until " + a.Until
			}
			fmt.Fprintf(w, "  %s  %s %s: %s%s\n", a.ID, a.Region, a.Resource, orNone(a.Reason), who)
		}
		fmt.Fprintln(w)
	}
	if len(r.ExpiredAcks) > 0 {
		fmt.Fprintf(w, "Acknowledgements that expired (their findings are back in the list):\n")
		for _, a := range r.ExpiredAcks {
			fmt.Fprintf(w, "  %s\n", a)
		}
		fmt.Fprintln(w)
	}
	if len(r.UnusedRules) > 0 {
		fmt.Fprintf(w, "Rules in %s that matched nothing (resource gone? remove them):\n", r.IgnoreFile)
		for _, a := range r.UnusedRules {
			fmt.Fprintf(w, "  %s\n", a)
		}
		fmt.Fprintln(w)
	}
}

func orNone(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

// PriceNoteFor describes where a run's prices came from (region -> source).
// Free runs (no sources) keep the built-in note.
func PriceNoteFor(sources map[string]string) string {
	if len(sources) == 0 {
		return PriceNote + " costhound Pro uses each region's own list prices from the AWS Price List."
	}
	var builtin []string
	fetched := ""
	for region, src := range sources {
		if strings.HasPrefix(src, "AWS Price List") {
			fetched = src
		} else {
			builtin = append(builtin, region)
		}
	}
	sort.Strings(builtin)
	switch {
	case len(builtin) == 0:
		return "Prices are each region's on-demand prices from the " + fetched + "; Cost Optimization Hub findings use AWS's own estimate."
	case fetched == "":
		return PriceNote + " (The AWS Price List couldn't be read, so the built-in prices were used.)"
	default:
		return "Prices are each region's on-demand prices from the " + fetched + ", except " + strings.Join(builtin, ", ") + " (built-in us-east-1 list prices)."
	}
}
