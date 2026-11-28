package pricing

import (
	"slices"
	"strings"
	"testing"
)

func TestEC2GravitonTwins(t *testing.T) {
	for typ, want := range map[string][]string{
		"m5.large":     {"m6g.large"},
		"m6i.large":    {"m7g.large", "m6g.large"},
		"m7i.2xlarge":  {"m7g.2xlarge"},
		"m5d.large":    {"m6gd.large"}, // 75 GB of instance storage -> 118 GB
		"c6in.large":   {"c7gn.large", "c6gn.large"},
		"t3.micro":     {"t4g.micro"},
		"m5.24xlarge":  nil, // m6g stops at 16xlarge
		"m6i.metal":    nil, // 128 vCPUs; m7g.metal has 64
		"c5n.large":    nil, // 5.25 GiB; c6gn.large has 4
		"m5zn.large":   nil, // high frequency: no twin
		"m4.large":     nil, // old-instance-type's
		"m7g.large":    nil, // already Graviton
		"nosuch.large": nil,
	} {
		if got := EC2GravitonTwins(typ); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", typ, got, want)
		}
	}
}

// Every twin the table can give has the same vCPUs and memory and at least
// the same instance storage, and every family on the list has one.
func TestEC2GravitonTwinsAreSameShape(t *testing.T) {
	found := map[string]bool{}
	for typ, src := range ec2Types {
		for _, tw := range EC2GravitonTwins(typ) {
			d := ec2Types[tw]
			if d.VCPU != src.VCPU || d.MemGiB != src.MemGiB || d.StoreGB < src.StoreGB || !strings.HasPrefix(d.CPU, "Graviton") || strings.HasPrefix(src.CPU, "Graviton") {
				t.Errorf("%s -> %s: %+v vs %+v", typ, tw, src, d)
			}
			fam, _, _ := strings.Cut(typ, ".")
			found[fam] = true
		}
	}
	for fam := range ec2Graviton {
		if !found[fam] {
			t.Errorf("family %s has no twin at any size", fam)
		}
	}
}

func TestEC2CurrentOptions(t *testing.T) {
	got := EC2CurrentOptions("m4.large")
	if len(got) == 0 || got[0] != "m6a.large" || !slices.Contains(got, "m7i.large") {
		t.Errorf("m4.large: %v, want m6a.large first (the cheapest) and m7i.large among them", got)
	}
	for _, typ := range []string{"c1.medium", "c1.xlarge", "m4.10xlarge", "g3.4xlarge", "m5.large"} {
		if got := EC2CurrentOptions(typ); len(got) != 0 {
			t.Errorf("%s: none at least as big of the same size (or not an old c/m/r/t type): %v", typ, got)
		}
	}
	if got := EC2CurrentOptions("m2.xlarge"); len(got) == 0 || !strings.HasPrefix(got[0], "r") {
		t.Errorf("m2 was memory-optimized: %v", got)
	}
}

// ec2-graviton and old-instance-type never judge the same family.
func TestGravitonAndOldFamiliesDontOverlap(t *testing.T) {
	for family := range ec2PreviousFamilies {
		if EC2GravitonFamily(family) {
			t.Errorf("%s is both old and a Graviton candidate", family)
		}
	}
}
