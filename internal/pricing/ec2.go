package pricing

import (
	"sort"
	"strings"
)

var ec2PreviousFamilies = map[string]string{
	"t1": "t3", "t2": "t3 (or t4g on ARM)",
	"m1": "m7i", "m2": "r7i", "m3": "m7i", "m4": "m7i (or m7g on ARM)",
	"c1": "c7i", "c3": "c7i", "c4": "c7i (or c7g on ARM)",
	"r3": "r7i", "r4": "r7i (or r7g on ARM)",
	"i2": "i4i", "d2": "d3", "g2": "g5", "g3": "g5", "p2": "g5", "x1": "x2iedn", "x1e": "x2iedn",
}

// EC2PreviousGeneration returns the suggested replacement family for an older EC2 type.
func EC2PreviousGeneration(typ string) string {
	family, _, _ := strings.Cut(typ, ".")
	return ec2PreviousFamilies[family]
}

// EC2 returns a type's shape and its us-east-1 Linux On-Demand price, for
// the types the built-in table keeps (ec2_table.go).
func EC2(typ string) (EC2Type, bool) {
	t, ok := ec2Types[typ]
	return t, ok
}

// EC2GravitonFamily says whether costhound compares an x86 family with
// Graviton.
func EC2GravitonFamily(family string) bool {
	_, ok := ec2Graviton[family]
	return ok
}

// EC2GravitonTwins lists the Graviton types an x86 type is compared with, in
// order of preference: the same size name, with the same vCPUs and memory
// and at least the same instance storage. None for a family costhound
// doesn't compare, a metal size (vCPU counts differ) or a size the twin
// family doesn't have (m5.24xlarge: m6g stops at 16xlarge).
func EC2GravitonTwins(typ string) []string {
	family, size, ok := strings.Cut(typ, ".")
	if !ok || strings.Contains(size, "metal") {
		return nil
	}
	src, ok := ec2Types[typ]
	if !ok || src.StoreGB < 0 {
		return nil
	}
	var out []string
	for _, tf := range ec2Graviton[family] {
		twin := tf + "." + size
		if t, ok := ec2Types[twin]; ok && t.VCPU == src.VCPU && t.MemGiB == src.MemGiB && t.StoreGB >= src.StoreGB {
			out = append(out, twin)
		}
	}
	return out
}

// ec2OldClass is the kind of current x86 type an old c, m, r or t family is
// compared with (m2 was memory-optimized).
var ec2OldClass = map[string]string{"t1": "t", "t2": "t", "m1": "m", "m2": "r", "m3": "m", "m4": "m",
	"c1": "c", "c3": "c", "c4": "c", "r3": "r", "r4": "r"}

// ec2CurrentX86 are the current x86 families (Intel and AMD, generations 6
// to 8, no flex types; t3 and t3a for the burstable ones) an old type is
// compared with.
var ec2CurrentX86 = map[string][]string{
	"m": {"m6i", "m6a", "m7i", "m7a", "m8i", "m8a"},
	"c": {"c6i", "c6a", "c7i", "c7a", "c8i", "c8a"},
	"r": {"r6i", "r6a", "r7i", "r7a", "r8i", "r8a"},
	"t": {"t3", "t3a"},
}

// EC2CurrentOptions lists the current x86 types of the same size name an old
// c, m, r or t type can move to, when they're at least as big in vCPUs and
// memory (c1.medium has 2 vCPUs and c7a.medium 1, so none), cheapest first at
// us-east-1 prices.
func EC2CurrentOptions(oldType string) []string {
	family, size, ok := strings.Cut(oldType, ".")
	cls, known := ec2OldClass[family]
	src, priced := ec2Types[oldType]
	if !ok || !known || !priced {
		return nil
	}
	var out []string
	for _, f := range ec2CurrentX86[cls] {
		t := f + "." + size
		if c, ok := ec2Types[t]; ok && c.VCPU >= src.VCPU && c.MemGiB >= src.MemGiB {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return ec2Types[out[i]].Hour < ec2Types[out[j]].Hour })
	return out
}
