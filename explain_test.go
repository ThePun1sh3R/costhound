package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

func TestExplain(t *testing.T) {
	f := checks.Finding{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-0abc (old-data)", Key: "vol-0abc",
		Detail: "gp2, 100 GiB, attached to nothing", MonthlyUSD: 10, Priced: true, Confidence: "high", Why: "attached to nothing",
		Evidence: []string{"EC2 DescribeVolumes: state available"}, Fix: []string{"aws ec2 delete-volume --volume-id vol-0abc"}}
	r := report.New("v", "111111111111", time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC), []string{"eu-west-1"}, []checks.Finding{f}, nil, 0)
	var js bytes.Buffer
	if err := r.JSON(&js); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "report.json")
	os.WriteFile(p, js.Bytes(), 0o600)
	id := r.Findings[0].ID

	var out bytes.Buffer
	if code := explain([]string{id, "-from", p}, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := out.String()
	for _, want := range []string{id + " · Unattached EBS volumes (free check)", "Cost: $10.00/month", "Confidence: high", "EC2 DescribeVolumes: state available",
		"Read with (read-only calls): ec2:DescribeVolumes", "aws ec2 delete-volume", "ignore:\n  - id: " + id} {
		if !strings.Contains(s, want) {
			t.Errorf("explain output misses %q\n%s", want, s)
		}
	}
	out.Reset()
	if code := explain([]string{"ch_nope", "-from", p}, &out); code != 2 {
		t.Errorf("an unknown ID is exit 2, got %d", code)
	}
}

func TestDetectReportKind(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"cloud":"azure","tenant_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","subscription_ids":["11111111-1111-1111-1111-111111111111"]}`, "azure"},
		{`{"cloud":"gcp","project_ids":["fixture-proj-a"]}`, "gcp"},
		{`{"account":"111111111111","findings":[]}`, "aws"},
		{`{"tenant_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","subscription_ids":["11111111-1111-1111-1111-111111111111"]}`, "azure"},
		{`{"project_ids":["fixture-proj-a"]}`, "gcp"},
	}
	for _, tc := range cases {
		if got := detectReportKind([]byte(tc.raw)); got != tc.want {
			t.Errorf("detectReportKind(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
