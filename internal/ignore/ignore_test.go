package ignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), ".costhound.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func sample() *report.Report {
	fs := []checks.Finding{
		{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-0abc111 (restore-drill)", Key: "vol-0abc111", MonthlyUSD: 100, Priced: true},
		{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-0zzz999", Key: "vol-0zzz999", MonthlyUSD: 50, Priced: true},
		{Check: "lambda-x86", Region: "eu-west-1", Resource: "3 function(s) on x86_64", Key: "lambda-on-x86"},
		{Check: "idle-ip", Region: "us-east-1", Resource: "1.2.3.4", Key: "1.2.3.4", MonthlyUSD: 3.65, Priced: true},
	}
	return report.New("v", "111111111111", time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC), []string{"eu-west-1", "us-east-1"}, fs, nil, 0)
}

func TestApply(t *testing.T) {
	r := sample()
	ipID := ""
	for _, f := range r.Findings {
		if f.Check == "idle-ip" {
			ipID = f.ID
		}
	}
	p := write(t, `ignore:
  - check: unattached-ebs
    resource: "vol-0abc*"
    region: eu-west-1
    reason: restore drills
    owner: platform
    until: 2027-03-01
  - check: lambda-x86
    reason: arm64 next quarter
  - id: `+ipID+`
    reason: old allowlisted IP
    until: 2026-11-01
  - check: idle-nat
    reason: nothing like this exists
`)
	f, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Apply(r, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	if len(r.Acknowledged) != 2 || r.Acknowledged[0].Owner != "platform" || r.Acknowledged[0].Until != "2027-03-01" {
		t.Fatalf("acknowledged = %+v", r.Acknowledged)
	}
	if len(r.Findings) != 2 || r.TotalMonthlyUSD != 53.65 {
		t.Fatalf("left %d findings, total %v: the matching vol and the lambda line leave the total", len(r.Findings), r.TotalMonthlyUSD)
	}
	if len(r.ExpiredAcks) != 1 || r.ExpiredAcks[0].ID != ipID {
		t.Fatalf("an expired id rule must bring its finding back and be listed: %+v", r.ExpiredAcks)
	}
	if len(r.UnusedRules) != 1 || r.UnusedRules[0].Check != "idle-nat" {
		t.Fatalf("unused = %+v", r.UnusedRules)
	}
	out := new(strings.Builder)
	r.Text(out, false)
	for _, want := range []string{"Acknowledged in " + p, "restore drills, platform, until 2027-03-01", "expired", "matched nothing"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text misses %q", want)
		}
	}
}

func TestUntilDayStillCounts(t *testing.T) {
	r := sample()
	f, _ := Load(write(t, "ignore:\n  - check: lambda-x86\n    until: 2026-11-19\n"), true)
	f.Apply(r, time.Date(2026, 11, 19, 23, 59, 0, 0, time.UTC))
	if len(r.Acknowledged) != 1 {
		t.Fatal("a rule applies through the whole until day")
	}
}

func TestLoadRefusesMistakes(t *testing.T) {
	for name, body := range map[string]string{
		"typo in a field":     "ignore:\n  - check: lambda-x86\n    untill: 2027-01-01\n",
		"matches everything":  "ignore:\n  - reason: everything\n",
		"unknown check":       "ignore:\n  - check: no-such-check\n",
		"bad date":            "ignore:\n  - check: lambda-x86\n    until: next year\n",
		"bad pattern":         "ignore:\n  - check: unattached-ebs\n    resource: \"vol-[\"\n",
		"empty owner tag":     "owner_tags: [\"\", team]\n",
		"alias of two owners": "owners:\n  payments: [pay-team]\n  billing: [Pay-Team]\n",
		"empty owner name":    "owners:\n  \"\": [x]\n",
	} {
		if _, err := Load(write(t, body), true); err == nil {
			t.Errorf("%s: loaded without error", name)
		}
	}
	if f, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), false); f != nil || err != nil {
		t.Error("a missing default file is fine")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), true); err == nil {
		t.Error("a missing explicit file is an error")
	}
}

// A rule written for idle-nat that names a NAT (by resource or by ID) keeps
// covering it after nat-no-routes took it over (checks.TookOver); a blanket
// check: idle-nat rule doesn't hide nat-no-routes findings.
func TestRuleForTheOldCheckCoversTheNewOne(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	nat := func(check, id string) checks.Finding {
		return checks.Finding{Check: check, Region: "eu-west-1", Resource: id, Key: id, MonthlyUSD: 32.85, Priced: true}
	}
	old := report.New("v", "111111111111", at, []string{"eu-west-1"}, []checks.Finding{nat("idle-nat", "nat-2")}, nil, 0)
	r := report.New("v", "111111111111", at, []string{"eu-west-1"}, []checks.Finding{nat("nat-no-routes", "nat-1"), nat("nat-no-routes", "nat-2"), nat("nat-no-routes", "nat-3")}, nil, 0)
	p := write(t, `ignore:
  - check: idle-nat
    resource: nat-1
    reason: standby for the cutover
  - id: `+old.Findings[0].ID+`
    reason: kept on purpose
  - check: idle-nat
    reason: written for idle NATs, not for NATs no route uses
`)
	f, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Apply(r, at)
	if len(r.Acknowledged) != 2 || len(r.Findings) != 1 || r.Findings[0].Key != "nat-3" || len(r.UnusedRules) != 1 || r.UnusedRules[0].Check != "idle-nat" {
		t.Fatalf("acknowledged %+v, left %+v, unused %+v", r.Acknowledged, r.Findings, r.UnusedRules)
	}
}

func TestOwnerSettings(t *testing.T) {
	f, err := Load(write(t, "owner_tags: [squad]\nowners:\n  payments: [pay-team, payments@example.com]\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.OwnerKeys()) != 1 || f.OwnerKeys()[0] != "squad" || len(f.OwnerAliases()["payments"]) != 2 {
		t.Errorf("owner settings: %+v", f)
	}
	var none *File
	if none.OwnerKeys() != nil || none.OwnerAliases() != nil {
		t.Error("no file means no owner settings")
	}
}
