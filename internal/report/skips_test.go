package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func dnsDownRegion(region string, extra ...inventory.Skip) []inventory.Skip {
	out := make([]inventory.Skip, 0, len(alwaysCalled)+len(extra))
	for _, c := range alwaysCalled {
		out = append(out, inventory.Skip{Region: region, What: c, Error: inventory.NoSuchHost})
	}
	return append(out, extra...)
}

func TestDNSNotOfferedWhenRegionReached(t *testing.T) {
	// DescribeVolumes etc. absent from skips ⇒ those calls succeeded.
	skips := []inventory.Skip{
		{Region: "eu-north-1", What: "apprunner:ListServices", Error: inventory.NoSuchHost},
		{Region: "eu-north-1", What: "appstream:DescribeFleets", Error: inventory.NoSuchHost},
		{Region: "eu-north-1", What: "docdb-elastic:ListClusters", Error: inventory.NoSuchHost},
		{Region: "eu-north-1", What: "workspaces:DescribeWorkspaces", Error: inventory.NoSuchHost},
	}
	r := New("v", "1", time.Now(), []string{"eu-north-1"}, nil, skips, 0)
	if len(r.Skipped) != 0 {
		t.Fatalf("DNS in a reached region must leave skipped: %+v", r.Skipped)
	}
	if len(r.NotOffered) != 1 || r.NotOffered[0].Region != "eu-north-1" {
		t.Fatalf("not_offered = %+v", r.NotOffered)
	}
	want := "App Runner, AppStream, DocumentDB Elastic, WorkSpaces"
	if got := strings.Join(r.NotOffered[0].Services, ", "); got != want {
		t.Fatalf("services = %q, want %q", got, want)
	}
	var buf bytes.Buffer
	WriteNotOffered(&buf, r.NotOffered)
	if got := buf.String(); got != "eu-north-1: "+want+" aren't offered here\n" {
		t.Fatalf("stderr line = %q", got)
	}
	var raw map[string]json.RawMessage
	var b bytes.Buffer
	if err := r.JSON(&b); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw["not_offered"], []byte(`"region": "eu-north-1"`)) ||
		!bytes.Contains(raw["not_offered"], []byte(`"App Runner"`)) ||
		!bytes.Equal(raw["skipped"], []byte("[]")) {
		t.Fatalf("JSON shape: not_offered=%s skipped=%s", raw["not_offered"], raw["skipped"])
	}
}

func TestDNSStaysSkipWhenNothingInRegionSucceeded(t *testing.T) {
	skips := dnsDownRegion("eu-north-1",
		inventory.Skip{Region: "eu-north-1", What: "apprunner:ListServices", Error: inventory.NoSuchHost},
	)
	r := New("v", "1", time.Now(), []string{"eu-north-1"}, nil, skips, 0)
	if len(r.NotOffered) != 0 {
		t.Fatalf("network-down DNS must not look like not-offered: %+v", r.NotOffered)
	}
	if len(r.Skipped) != len(skips) {
		t.Fatalf("skipped = %d, want %d: %+v", len(r.Skipped), len(skips), r.Skipped)
	}
}

func TestComprehendNotOffered(t *testing.T) {
	const op = "comprehend:ListEndpoints"
	r := New("v", "1", time.Now(), []string{"eu-north-1", "eu-west-1"}, nil, []inventory.Skip{
		{Region: "eu-north-1", What: op, Error: inventory.NoSuchHost},
		{Region: "eu-west-1", What: op, Error: "AccessDeniedException"},
	}, 0)
	if len(r.NotOffered) != 1 || r.NotOffered[0].Region != "eu-north-1" || strings.Join(r.NotOffered[0].Services, ",") != "Comprehend" ||
		len(r.Skipped) != 1 || r.Skipped[0].Region != "eu-west-1" {
		t.Fatalf("not offered = %+v, unread = %+v", r.NotOffered, r.Skipped)
	}
}

func TestAccessDeniedAndTimeoutStaySkips(t *testing.T) {
	skips := []inventory.Skip{
		{Region: "eu-north-1", What: "apprunner:ListServices", Error: "AccessDenied"},
		{Region: "eu-north-1", What: "appstream:DescribeFleets", Error: "context deadline exceeded"},
		{Region: "eu-north-1", What: "ec2:DescribeVolumes", Error: "UnauthorizedOperation"},
	}
	r := New("v", "1", time.Now(), []string{"eu-north-1"}, nil, skips, 0)
	if len(r.NotOffered) != 0 {
		t.Fatalf("non-DNS errors must stay skips, not_offered=%+v", r.NotOffered)
	}
	if len(r.Skipped) != 3 {
		t.Fatalf("skipped = %+v", r.Skipped)
	}
}
