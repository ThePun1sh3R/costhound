package checks

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ec2Prices gives the hourly prices of the types from one source: the
// region's Price List (paid scans) when it has all of them, else AWS's
// us-east-1 list; nil when that misses one too.
func ec2Prices(r *inventory.Region, types []string) (map[string]float64, string) {
	if out, source, ok := paidEC2Prices(r, types); ok {
		return out, source
	}
	out := map[string]float64{}
	for _, t := range types {
		s, ok := pricing.EC2(t)
		if !ok {
			return nil, ""
		}
		out[t] = s.Hour
	}
	return out, "us-east-1 list prices"
}

// gravitonPrices gives both hourly prices from one source (ec2Prices).
func gravitonPrices(r *inventory.Region, typ, twin string) (now, then float64, source string) {
	p, src := ec2Prices(r, []string{typ, twin})
	if p == nil {
		return 0, 0, ""
	}
	return p[typ], p[twin], src
}

// gravitonMove is instances of one type and the Graviton type they can move
// to, priced from one source.
type gravitonMove struct {
	members   []ec2types.Instance // sorted by ID
	typ, twin string
	azs       []string
	older     bool // the twin is the fallback, a Graviton generation older
	now, then float64
	source    string
}

// gravitonPick picks, for instances all of one type, the first twin offered
// in every zone they run in; false when there's none, or it costs no less.
func gravitonPick(r *inventory.Region, members []ec2types.Instance) (gravitonMove, bool) {
	slices.SortFunc(members, func(a, b ec2types.Instance) int {
		return strings.Compare(aws.ToString(a.InstanceId), aws.ToString(b.InstanceId))
	})
	m := gravitonMove{members: members, typ: string(members[0].InstanceType)}
	for _, in := range members {
		if az := aws.ToString(in.Placement.AvailabilityZone); !slices.Contains(m.azs, az) {
			m.azs = append(m.azs, az)
		}
	}
	slices.Sort(m.azs)
	for i, tw := range pricing.EC2GravitonTwins(m.typ) {
		if !slices.ContainsFunc(m.azs, func(az string) bool { return !r.Offered[az][tw] }) {
			m.twin, m.older = tw, i > 0
			break
		}
	}
	if m.twin == "" {
		return m, false
	}
	m.now, m.then, m.source = gravitonPrices(r, m.typ, m.twin)
	return m, m.then > 0 && m.then < m.now
}

// EC2Graviton finds running Linux x86 instances whose Graviton type of the
// same size is offered in their Availability Zone and costs less. An Auto
// Scaling group is reported once, all its types together (Key asg/<name>):
// the group replaces instances and may change their type, and neither must
// read as a fix. Always low: everything on the instance needs an arm64
// build, since an x86 AMI doesn't boot on Graviton, and a Reserved Instance
// or Savings Plan for the old family shrinks the saving.
func EC2Graviton(r *inventory.Region, o Options) []Finding {
	if r.Offered == nil {
		return nil // which zones offer which types is unknown
	}
	var out []Finding
	groups := map[string][]ec2types.Instance{}
	var order []string
	for _, in := range r.Instances {
		if !inventory.GravitonCandidate(in, r.Name) || ec2LooksIdle(r, in, o) {
			continue
		}
		if asg, ok := tag(in.Tags, "aws:autoscaling:groupName"); ok && asg != "" {
			if _, seen := groups[asg]; !seen {
				order = append(order, asg)
			}
			groups[asg] = append(groups[asg], in)
			continue
		}
		if m, ok := gravitonPick(r, []ec2types.Instance{in}); ok {
			out = append(out, gravitonFinding(r, o, []gravitonMove{m}, ""))
		}
	}
	defer gravitonUnjudged(r, &out)
	for _, asg := range order {
		byType := map[string][]ec2types.Instance{}
		var types []string
		for _, in := range groups[asg] {
			t := string(in.InstanceType)
			if _, seen := byType[t]; !seen {
				types = append(types, t)
			}
			byType[t] = append(byType[t], in)
		}
		slices.Sort(types)
		var moves []gravitonMove
		for _, t := range types {
			if m, ok := gravitonPick(r, byType[t]); ok {
				moves = append(moves, m)
			}
		}
		if len(moves) > 0 {
			out = append(out, gravitonFinding(r, o, moves, asg))
		}
	}
	return out
}

// gravitonFinding reports one instance (asg "") or an Auto Scaling group's
// moves.
func gravitonFinding(r *inventory.Region, o Options, moves []gravitonMove, asg string) Finding {
	f := Finding{Check: "ec2-graviton", Region: r.Name, Confidence: "low", Priced: true}
	var froms, tos, ids, details, families []string
	n := 0
	var notes []string
	note := func(s string) {
		if !slices.Contains(notes, s) {
			notes = append(notes, s)
		}
	}
	for _, m := range moves {
		k := len(m.members)
		n += k
		f.MonthlyUSD += (m.now - m.then) * float64(k) * pricing.HoursPerMonth
		froms, tos = append(froms, m.typ), append(tos, m.twin)
		for _, in := range m.members {
			id := aws.ToString(in.InstanceId)
			ids = append(ids, id)
			if asg != "" && BuildHasPro {
				if f.EC2MemberSavings == nil {
					f.EC2MemberSavings = map[string]float64{}
				}
				f.EC2MemberSavings[id] = (m.now - m.then) * pricing.HoursPerMonth
			}
		}
		family, _, _ := strings.Cut(m.typ, ".")
		if !slices.Contains(families, family) {
			families = append(families, family)
		}
		src, _ := pricing.EC2(m.typ)
		dst, _ := pricing.EC2(m.twin)
		details = append(details, fmt.Sprintf("%d x %s: %s costs $%.4f instead of $%.4f an hour each (%s)", k, m.typ, m.twin, m.then, m.now, m.source))
		f.Evidence = append(f.Evidence,
			fmt.Sprintf("EC2 DescribeInstanceTypeOfferings: %s is offered in %s", m.twin, strings.Join(m.azs, ", ")),
			fmt.Sprintf("%s: %s $%.4f an hour, %s $%.4f (both %d vCPUs, %g GiB)", m.source, m.typ, m.now, m.twin, m.then, src.VCPU, src.MemGiB))
		if m.older {
			note(fmt.Sprintf("%s is %s, a generation older than %s: check the workload's speed on it", m.twin, dst.CPU, m.typ))
		}
		if g := strings.TrimLeft(family, "cmrt"); strings.HasSuffix(family, "a") && (strings.HasPrefix(g, "7") || strings.HasPrefix(g, "8")) {
			note("AMD vCPUs on " + family + " are whole cores, as Graviton's are: compare the speed per vCPU")
		}
		if strings.HasPrefix(family, "t") {
			note("a burstable type: a slower core can use more CPU credits, and in unlimited mode the surplus is billed")
		}
		if src.Disks > 0 && dst.Disks != src.Disks {
			note(fmt.Sprintf("%s's instance storage is split differently: %d GB on %d disk(s), %d GB on %d disk(s) on %s", m.typ, src.StoreGB, src.Disks, dst.StoreGB, dst.Disks, m.twin))
		}
	}
	slices.Sort(ids)
	first := moves[0].members[0]
	if asg == "" {
		m := moves[0]
		id := aws.ToString(first.InstanceId)
		f.Resource = label(id, first.Tags)
		f.Facts = facts("instance", id, "from_type", m.typ, "to_type", m.twin)
		f.Detail = fmt.Sprintf("%s instance: the Graviton type %s of the same size costs $%.4f instead of $%.4f an hour (%s)", m.typ, m.twin, m.then, m.now, m.source)
		f.Evidence = append([]string{fmt.Sprintf("EC2 DescribeInstances: %s, x86_64, Linux/UNIX On-Demand, running since %s", m.typ, dateOf(first.LaunchTime))}, f.Evidence...)
		f.Fix = []string{
			"# every program on it needs an arm64 build: an arm64 AMI, arm64 container images, native libraries",
			fmt.Sprintf("# launch a %s from that AMI, move the work over and test it, then stop this instance", m.twin),
			"# changing the type in place fails: an x86 AMI doesn't boot on Graviton",
		}
	} else {
		f.Resource = fmt.Sprintf("%s (Auto Scaling group)", asg)
		f.Key = "asg/" + asg
		f.Members = ids
		f.Facts = facts("asg", asg, "instances", itoa(n), "sample", ids[0], "from_type", strings.Join(froms, ", "), "to_type", strings.Join(tos, ", "))
		f.Detail = fmt.Sprintf("%d instance(s) of Auto Scaling group %s could move to the same-size Graviton type: %s", n, asg, strings.Join(details, "; "))
		f.Evidence = append([]string{fmt.Sprintf("EC2 DescribeInstances: %d running instance(s) of Auto Scaling group %s, x86_64, Linux/UNIX On-Demand", n, asg)}, f.Evidence...)
		f.Fix = []string{
			"# every program on its instances needs an arm64 build: an arm64 AMI, arm64 container images, native libraries",
			fmt.Sprintf("# then add a launch template version with that AMI and %s, point the group at it, and start an instance refresh", strings.Join(tos, " / ")),
		}
		if ng, ok := tag(first.Tags, "eks:nodegroup-name"); ok && ng != "" {
			cluster, _ := tag(first.Tags, "eks:cluster-name")
			f.Facts["node_group"], f.Facts["eks_cluster"] = ng, cluster
			f.Fix = []string{
				"# an EKS managed node group: every container image its pods run needs an arm64 build (a multi-arch image)",
				fmt.Sprintf("# then create a node group with an arm64 AMI type (AL2023_ARM_64_STANDARD or BOTTLEROCKET_ARM_64) and %s, move the pods to it, and delete the old one", strings.Join(tos, " / ")),
			}
		}
	}
	whys := []string{
		"every program on it needs an arm64 build (an x86 AMI doesn't boot on Graviton)",
		fmt.Sprintf("priced On-Demand: a Reserved Instance or Savings Plan covering %s shrinks the saving until it ends", strings.Join(families, ", ")),
	}
	var ris []string
	for _, family := range families {
		if text, id := gravitonRegionReservation(r, family, o); text != "" {
			whys, ris = append(whys, text), append(ris, id)
		}
	}
	if len(ris) > 0 {
		f.Facts["reservation"] = strings.Join(ris, ", ")
	}
	f.Why = strings.Join(append(whys, notes...), "; ")
	return f
}

// gravitonUnjudged records the Auto Scaling groups that exist with no
// finding now, unless every running member is on Graviton already (that's
// the fix): scaled to zero, all Spot for now, a member in a zone without the
// twin, an x86 type with no twin. Their earlier finding is parked, not fixed.
func gravitonUnjudged(r *inventory.Region, out *[]Finding) {
	if r.ASGUnknown {
		return // the groups weren't read: ec2-graviton's sources say so
	}
	reported := map[string]bool{}
	for _, f := range *out {
		if f.Key != "" {
			reported[f.Key] = true
		}
	}
	running, arm := map[string]int{}, map[string]int{}
	for _, in := range r.Instances {
		g, ok := tag(in.Tags, "aws:autoscaling:groupName")
		if !ok || g == "" || in.State == nil || in.State.Name != ec2types.InstanceStateNameRunning {
			continue
		}
		running[g]++
		if in.Architecture == ec2types.ArchitectureValuesArm64 {
			arm[g]++
		}
	}
	for _, name := range r.ASGNames {
		key := "asg/" + name
		if reported[key] || (running[name] > 0 && arm[name] == running[name]) {
			continue
		}
		r.Unjudged = append(r.Unjudged, "ec2-graviton|"+key)
	}
}
