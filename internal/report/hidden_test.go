package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

// -min-usd hides small findings, and the report says what it hid: how many,
// what they cost together, by check, and how to see them.
func TestNewCountsWhatItHides(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	fs := []checks.Finding{
		fnd("orphan-alarm", "eu-west-1", "alarm-1", 0.10), fnd("orphan-alarm", "eu-west-1", "alarm-2", 0.10), fnd("orphan-alarm", "us-east-1", "alarm-3", 0.10),
		fnd("unused-secret", "eu-west-1", "db-pass", 0.40),
		fnd("unattached-ebs", "eu-west-1", "vol-1", 5),
		{Check: "old-instance-type", Region: "eu-west-1", Resource: "i-1"}, // unpriced: never hidden
	}
	r := New("v", "123456789012", at, []string{"eu-west-1", "us-east-1"}, fs, nil, 0.5)
	h := r.Hidden
	if len(r.Findings) != 2 || h == nil || h.Count != 4 || !near(h.MonthlyUSD, 0.70) || h.MinUSD != 0.5 || len(h.Keys) != 4 {
		t.Fatalf("hidden: %+v, shown %d", h, len(r.Findings))
	}
	if h.ByCheck[0].Check != "unused-secret" || h.ByCheck[1].Check != "orphan-alarm" || h.ByCheck[1].Count != 3 || !near(h.ByCheck[1].MonthlyUSD, 0.30) {
		t.Errorf("by check, biggest first: %+v", h.ByCheck)
	}
	var b bytes.Buffer
	r.Text(&b, false)
	want := "Hidden: 4 finding(s) under $0.50/month each, $0.70/month together (unused-secret 1, $0.40; orphan-alarm 3, $0.30): -min-usd 0 shows them"
	if !strings.Contains(b.String(), want) {
		t.Errorf("text lacks %q:\n%s", want, b.String())
	}
	j, _ := json.Marshal(r)
	if !strings.Contains(string(j), `"hidden":{"min_usd":0.5,"count":4,`) {
		t.Errorf("JSON: %s", j)
	}

	// Only small findings: the report doesn't say it found nothing.
	b.Reset()
	New("v", "123456789012", at, []string{"eu-west-1"}, fs[:3], nil, 0.5).Text(&b, false)
	if out := b.String(); strings.Contains(out, "Nice") || !strings.Contains(out, "Nothing at or above $0.50/month by these checks.\nHidden: 3 finding(s)") {
		t.Errorf("only hidden findings:\n%s", out)
	}

	// -min-usd 0 hides nothing, and says nothing.
	b.Reset()
	r0 := New("v", "123456789012", at, []string{"eu-west-1"}, fs[:3], nil, 0)
	r0.Text(&b, false)
	if r0.Hidden != nil || strings.Contains(b.String(), "Hidden:") {
		t.Errorf("nothing hidden: %+v", r0.Hidden)
	}
}

// Small-by-nature checks are judged by their region's total: six orphan
// alarms at $0.10 show, two don't; two unused secrets show, one doesn't.
// Every other check keeps the per-finding rule.
func TestSmallEachCountsByRegion(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var fs []checks.Finding
	for i := 0; i < 6; i++ {
		fs = append(fs, fnd("orphan-alarm", "eu-west-1", fmt.Sprintf("a%d", i), 0.10))
	}
	fs = append(fs, fnd("orphan-alarm", "us-east-1", "b1", 0.10), fnd("orphan-alarm", "us-east-1", "b2", 0.10),
		fnd("unused-secret", "eu-west-1", "s1", 0.40), fnd("unused-secret", "us-east-1", "s2", 0.40), fnd("unused-secret", "us-east-1", "s3", 0.40),
		fnd("idle-ip", "eu-west-1", "1.2.3.4", 0.40), fnd("idle-ip", "eu-west-1", "1.2.3.5", 0.40))
	r := New("v", "123456789012", at, []string{"eu-west-1", "us-east-1"}, fs, nil, 0.5)
	shown := map[string]int{}
	for _, f := range r.Findings {
		shown[f.Check+" "+f.Region]++
	}
	want := map[string]int{"orphan-alarm eu-west-1": 6, "unused-secret us-east-1": 2}
	if fmt.Sprint(shown) != fmt.Sprint(want) || r.Hidden == nil || r.Hidden.Count != 5 {
		t.Fatalf("shown %v (want %v), hidden %+v", shown, want, r.Hidden)
	}
	if !near(r.TotalMonthlyUSD, 1.40) {
		t.Errorf("total %v", r.TotalMonthlyUSD)
	}
	for c := range smallEach {
		if checks.Title(c) == c {
			t.Errorf("%s is not a check", c)
		}
	}
}
