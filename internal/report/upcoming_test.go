package report

import (
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

func TestUpcoming(t *testing.T) {
	d := func(s string) time.Time { x, _ := time.Parse("2006-01-02", s); return x }
	r := New("v", "1", time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), []string{"eu-west-1"}, nil, nil, 0)
	r.SetUpcoming([]checks.Cliff{
		{Date: d("2027-03-28"), Region: "eu-west-1", Resource: "prod", What: "Kubernetes 1.30 leaves extended support", Outcome: "EKS upgrades the cluster", MonthlyUSD: -365, Priced: true},
		{Date: d("2027-01-06"), Region: "eu-west-1", Resource: "prod", What: "Kubernetes 1.30 leaves standard support", Outcome: "the control plane costs more", MonthlyUSD: 365, Priced: true},
		{Date: d("2027-01-20"), Region: "eu-west-1", Resource: "orders", What: "postgres 13 leaves standard support", Outcome: "extended support is billed", MonthlyUSD: 146, Priced: true},
		{Date: d("2026-12-17"), Region: "eu-west-1", Resource: "auto", What: "Kubernetes 1.31 leaves standard support", Outcome: "EKS upgrades it"},
	})
	if r.Upcoming[0].Resource != "auto" || r.Upcoming[3].Date != d("2027-03-28") {
		t.Fatalf("earliest first: %+v", r.Upcoming)
	}
	months, total := MonthSteps(r.Upcoming)
	if strings.Join(months, " ") != "2027-01 2027-03" || total[0] != 511 || total[1] != 146 {
		t.Errorf("running total by month: %v %v", months, total)
	}
	var b strings.Builder
	r.Text(&b, false)
	for _, want := range []string{"Coming up, if nothing changes", "2027-01-06  eu-west-1      prod: Kubernetes 1.30 leaves standard support",
		"(+$365.00/mo)", "(-$365.00/mo)", "Monthly bill change, running total: 2027-01 +$511.00 2027-03 +$146.00"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, b.String())
		}
	}
}
