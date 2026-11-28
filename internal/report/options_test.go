package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

func TestOptionsExcludedFromTotal(t *testing.T) {
	fs := []checks.Finding{
		{Check: "idle-ip", Region: "eu-west-1", Resource: "1.2.3.4", MonthlyUSD: 3.65, Priced: true},
		{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1", MonthlyUSD: 10, Priced: true},
	}
	r := New("v", "111111111111", time.Now(), []string{"eu-west-1"}, fs, nil, 0)
	want := r.TotalMonthlyUSD
	r.SetOptions([]checks.Option{
		{Kind: "nat-instance", Region: "eu-west-1", Resource: "nat-0abc", CurrentMonthlyUSD: 50, AlternativeMonthlyUSD: 15, SavingMonthlyUSD: 35, UpkeepHours: 2, BreakEvenUSDPerHour: 17.5},
	})
	if r.TotalMonthlyUSD != want {
		t.Fatalf("total must ignore options: %v vs %v", r.TotalMonthlyUSD, want)
	}
}

func TestOptionsInReportOutput(t *testing.T) {
	r := New("v", "111111111111", time.Now(), []string{"eu-west-1"}, nil, nil, 0)
	r.SetOptions([]checks.Option{
		{Kind: "nat-instance", Region: "eu-west-1", Resource: "nat-1", Current: "NAT gateway", Alternative: "NAT instance on t4g.small",
			CurrentMonthlyUSD: 41.2, AlternativeMonthlyUSD: 13.1, SavingMonthlyUSD: 28.1, UpkeepHours: 2, BreakEvenUSDPerHour: 14.05,
			Tradeoffs: []string{"no managed failover"}, Assumptions: []string{"traffic stays like the measured window"}},
	})
	var text bytes.Buffer
	r.Text(&text, true)
	if !strings.Contains(text.String(), "Cheaper ways to run what you already use") {
		t.Fatal("text section missing")
	}
	b, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(b), `"options"`) {
		t.Fatalf("json: %v %s", err, b)
	}
}
