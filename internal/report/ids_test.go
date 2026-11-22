package report

import (
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

func TestFindingIDStable(t *testing.T) {
	a := checks.Finding{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1 (old)", Key: "vol-1"}
	b := checks.Finding{Check: "unattached-ebs", Region: "eu-west-1", Resource: "vol-1 (renamed)", Key: "vol-1"}
	if FindingID("111111111111", a) != FindingID("111111111111", b) {
		t.Fatal("renaming a tag must keep the ID")
	}
	if FindingID("111111111111", a) == FindingID("222222222222", a) {
		t.Fatal("the same resource name in another account is another finding")
	}
	if id := FindingID("1", a); len(id) != 13 || id[:3] != "ch_" {
		t.Fatalf("id %q", id)
	}
}
