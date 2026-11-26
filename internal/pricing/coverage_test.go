package pricing

import (
	"fmt"
	"strings"
	"testing"
)

func TestEC2AndGravitonFamily(t *testing.T) {
	typ, ok := EC2("m5.large")
	want := ec2Types["m5.large"]
	if !ok || typ != want {
		t.Fatalf("EC2(m5.large) = %+v %v, want %+v", typ, ok, want)
	}
	if _, ok := EC2("m5.no-such-size"); ok {
		t.Fatal("unknown type must be not found")
	}
	if _, ok := EC2("zzzz.large"); ok {
		t.Fatal("unknown family must be not found")
	}
	for fam := range ec2Graviton {
		if !EC2GravitonFamily(fam) {
			t.Errorf("family %s must be compared with Graviton", fam)
		}
	}
	for _, fam := range []string{"m4", "m7g", "nosuch", "m5.metal", ""} {
		if EC2GravitonFamily(fam) {
			t.Errorf("%q must not be a graviton-compared family", fam)
		}
	}
}

func TestDedicatedHostHour(t *testing.T) {
	for fam, want := range dedicatedHostHour {
		got, ok := DedicatedHostHour(fam)
		if !ok || got != want {
			t.Errorf("DedicatedHostHour(%s) = %v %v, want %v", fam, got, ok, want)
		}
	}
	if _, ok := DedicatedHostHour("nope"); ok {
		t.Fatal("unknown family must be not found")
	}
	if _, ok := DedicatedHostHour("m5.large"); ok {
		t.Fatal("instance type is not a host family")
	}
}

func TestDMSHour(t *testing.T) {
	rate := dmsHours["dms.t3.medium"]
	if got := DMSHour("dms.t3.medium", false); got != rate.single || got == 0 {
		t.Fatalf("single = %v, want %v", got, rate.single)
	}
	if got := DMSHour("dms.t3.medium", true); got != rate.multi {
		t.Fatalf("multi = %v, want %v", got, rate.multi)
	}
	if DMSHour("dms.no.such", false) != 0 || DMSHour("dms.no.such", true) != 0 {
		t.Fatal("unknown class must miss the table (zero)")
	}
}

func TestPCAMonthly(t *testing.T) {
	if PCAMonthly("SHORT_LIVED_CERTIFICATE") != PCAShortLivedMonthly {
		t.Fatal("short lived")
	}
	if PCAMonthly("GENERAL_PURPOSE") != PCAGeneralPurposeMonthly {
		t.Fatal("general purpose")
	}
	if PCAMonthly("anything-else") != PCAGeneralPurposeMonthly {
		t.Fatal("unknown mode uses general purpose")
	}
}

func TestDynamoDBProvisionedMonthly(t *testing.T) {
	if DynamoDBProvisionedMonthly(0, 0) != 0 {
		t.Fatal("zero capacity must be free")
	}
	rcu, wcu := int64(10), int64(5)
	want := (float64(rcu)*DynamoDBRCUHour + float64(wcu)*DynamoDBWCUHour) * HoursPerMonth
	if got := DynamoDBProvisionedMonthly(rcu, wcu); !near(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	large := DynamoDBProvisionedMonthly(1_000_000, 500_000)
	wantLarge := (1_000_000*DynamoDBRCUHour + 500_000*DynamoDBWCUHour) * HoursPerMonth
	if !near(large, wantLarge) || large <= want {
		t.Fatalf("large: %v want %v", large, wantLarge)
	}
}

func TestEKSControlPlaneHour(t *testing.T) {
	for tier, want := range eksTierHour {
		got, ok := EKSControlPlaneHour(tier)
		if !ok || got != want {
			t.Errorf("%s: %v %v, want %v", tier, got, ok, want)
		}
	}
	got, ok := EKSControlPlaneHour("")
	if !ok || got != EKSClusterHour {
		t.Fatalf("empty tier defaults to standard: %v %v", got, ok)
	}
	if _, ok := EKSControlPlaneHour("tier-nope"); ok {
		t.Fatal("unknown tier must be not found")
	}
}

func TestWorkspaceMonthly(t *testing.T) {
	for key, want := range workspaceMonths {
		parts := strings.Split(key, "|")
		if len(parts) != 4 {
			t.Fatalf("bad table key %q", key)
		}
		var root, user int32
		if _, err := fmt.Sscanf(parts[2]+" "+parts[3], "%d %d", &root, &user); err != nil {
			t.Fatal(err)
		}
		if got := WorkspaceMonthly(parts[0], parts[1], root, user); got != want || want == 0 {
			t.Errorf("%s: %v, want %v", key, got, want)
		}
	}
	if WorkspaceMonthly("ALWAYS_ON", "VALUE", 1, 1) != 0 {
		t.Fatal("unknown combo must be unpriced (0)")
	}
	if WorkspaceMonthly("NOPE", "VALUE", 80, 10) != 0 {
		t.Fatal("unknown mode must be unpriced")
	}
}
