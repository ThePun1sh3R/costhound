package checks

import (
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/pricing"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// x86 is a running Linux/UNIX On-Demand x86 instance, 90 days old, with tags
// given as key, value pairs.
func x86(id, typ, az string, kv ...string) ec2types.Instance {
	in := ec2types.Instance{InstanceId: aws.String(id), InstanceType: ec2types.InstanceType(typ),
		State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, LaunchTime: ago(90),
		Architecture: ec2types.ArchitectureValuesX8664, PlatformDetails: aws.String("Linux/UNIX"), UsageOperation: aws.String("RunInstances"),
		Placement: &ec2types.Placement{AvailabilityZone: aws.String(az), Tenancy: ec2types.TenancyDefault}}
	for i := 0; i+1 < len(kv); i += 2 {
		in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}
	return in
}

func TestEC2Graviton(t *testing.T) {
	got := byResource(EC2Graviton(fixture(), opts()))
	f := got["i-x86-web"]
	if f.Confidence != "low" || f.Facts["from_type"] != "m5.large" || f.Facts["to_type"] != "m6g.large" || f.Facts["instance"] != "i-x86-web" {
		t.Fatalf("the x86 instance: %+v", got)
	}
	wantUSD(t, f, (0.096-0.077)*730) // us-east-1 list prices
	if !strings.Contains(f.Detail, "us-east-1 list prices") || !strings.Contains(f.Why, "arm64 build") || !strings.Contains(f.Why, "Savings Plan covering m5") {
		t.Errorf("detail and why: %q / %q", f.Detail, f.Why)
	}
	for _, l := range f.Fix {
		if !strings.HasPrefix(l, "#") {
			t.Errorf("a rebuild for arm64 can't be a command: comments only: %q", f.Fix)
		}
	}
	// The group: once, keyed by group and type, priced for both instances.
	g := got["web-x86 (Auto Scaling group)"]
	if g.Key != "asg/web-x86" || g.Facts["instances"] != "2" || g.Facts["sample"] != "i-web-1" || g.Facts["to_type"] != "m7g.large" ||
		strings.Join(g.Members, " ") != "i-web-1 i-web-2" {
		t.Fatalf("the group: %+v", got)
	}
	wantUSD(t, g, 2*(0.096-0.0816)*730)
	if !strings.Contains(strings.Join(g.Fix, " "), "instance refresh") {
		t.Errorf("a group changes through its launch template: %q", g.Fix)
	}
	if len(got) != 2 {
		t.Errorf("only those two: %v", got)
	}
}

// Every instance ec2-graviton must leave alone, one change at a time.
func TestEC2GravitonSkips(t *testing.T) {
	for name, change := range map[string]func(*ec2types.Instance){
		"RHEL (billed apart)": func(in *ec2types.Instance) {
			in.UsageOperation = aws.String("RunInstances:0010")
			in.PlatformDetails = aws.String("Red Hat Enterprise Linux")
		},
		"Spot":                   func(in *ec2types.Instance) { in.InstanceLifecycle = ec2types.InstanceLifecycleTypeSpot },
		"a Capacity Reservation": func(in *ec2types.Instance) { in.CapacityReservationId = aws.String("cr-1") },
		"dedicated tenancy":      func(in *ec2types.Instance) { in.Placement.Tenancy = ec2types.TenancyDedicated },
		"a Marketplace product": func(in *ec2types.Instance) {
			in.ProductCodes = []ec2types.ProductCode{{ProductCodeId: aws.String("abc")}}
		},
		"a Local Zone":  func(in *ec2types.Instance) { in.Placement.AvailabilityZone = aws.String("eu-west-1-lhr-1a") },
		"already arm64": func(in *ec2types.Instance) { in.Architecture = ec2types.ArchitectureValuesArm64 },
		"an EMR node": func(in *ec2types.Instance) {
			in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String("aws:elasticmapreduce:job-flow-id"), Value: aws.String("j-1")})
		},
		"a Karpenter node": func(in *ec2types.Instance) {
			in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String("karpenter.sh/nodepool"), Value: aws.String("p")})
		},
		"a fleet member": func(in *ec2types.Instance) {
			in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String("aws:ec2:fleet-id"), Value: aws.String("fleet-1")})
		},
		"run by an AWS service":               func(in *ec2types.Instance) { in.Operator = &ec2types.OperatorResponse{Managed: aws.Bool(true)} },
		"stopped":                             func(in *ec2types.Instance) { in.State.Name = ec2types.InstanceStateNameStopped },
		"an old family (old-instance-type's)": func(in *ec2types.Instance) { in.InstanceType = "m4.large" },
		"a metal size":                        func(in *ec2types.Instance) { in.InstanceType = "m5.metal" },
		"a size m6g lacks":                    func(in *ec2types.Instance) { in.InstanceType = "m5.24xlarge" },
		"no twin in its zone":                 func(in *ec2types.Instance) { in.Placement.AvailabilityZone = aws.String("eu-west-1c") },
	} {
		r := fixture()
		for i := range r.Instances {
			if aws.ToString(r.Instances[i].InstanceId) == "i-x86-web" {
				change(&r.Instances[i])
			}
		}
		if f, ok := byResource(EC2Graviton(r, opts()))["i-x86-web"]; ok {
			t.Errorf("%s: no finding, got %+v", name, f)
		}
	}
	r := fixture()
	r.Offered = nil // the offerings read failed
	if got := EC2Graviton(r, opts()); len(got) != 0 {
		t.Errorf("unknown offerings: nothing, got %+v", got)
	}
}

// m6i's first choice is m7g; where only m6g is offered, m6g, and the Why says
// it's a generation older. A group takes a twin every zone of it offers.
func TestEC2GravitonFallsBackAGeneration(t *testing.T) {
	r := fixture()
	r.Offered["eu-west-1b"] = map[string]bool{"m6g.large": true}
	g := byResource(EC2Graviton(r, opts()))["web-x86 (Auto Scaling group)"]
	if g.Facts["to_type"] != "m6g.large" || !strings.Contains(g.Why, "Graviton2, a generation older than m6i.large") {
		t.Errorf("m7g isn't offered in eu-west-1b: %+v", g)
	}
	wantUSD(t, g, 2*(0.096-0.077)*730)
}

func TestEC2GravitonNotes(t *testing.T) {
	r := fixture()
	r.Instances = []ec2types.Instance{
		x86("i-burst", "t3.medium", "eu-west-1a"),
		x86("i-amd", "m7a.large", "eu-west-1a"),
		x86("i-ng", "m5.large", "eu-west-1a", "aws:autoscaling:groupName", "eks-ng-1", "eks:nodegroup-name", "ng-1", "eks:cluster-name", "prod"),
	}
	r.Offered = map[string]map[string]bool{"eu-west-1a": {"t4g.medium": true, "m7g.large": true, "m6g.large": true}}
	got := byResource(EC2Graviton(r, opts()))
	if f := got["i-burst"]; f.Facts["to_type"] != "t4g.medium" || !strings.Contains(f.Why, "CPU credits") {
		t.Errorf("burstable: %+v", f)
	}
	if f := got["i-amd"]; !strings.Contains(f.Why, "whole cores") {
		t.Errorf("m7a's vCPUs are whole cores: %+v", f)
	}
	ng := got["eks-ng-1 (Auto Scaling group)"]
	if ng.Facts["node_group"] != "ng-1" || ng.Facts["eks_cluster"] != "prod" || !strings.Contains(strings.Join(ng.Fix, " "), "AL2023_ARM_64_STANDARD") {
		t.Errorf("an EKS managed node group: %+v", ng)
	}
}

// ec2-graviton and old-instance-type never judge the same family.
func TestGravitonAndOldFamiliesDontOverlap(t *testing.T) {
	for f := range oldFamilies {
		if pricing.EC2GravitonFamily(f) {
			t.Errorf("%s is both old and a Graviton candidate", f)
		}
	}
}

func TestOldInstanceTypesPriced(t *testing.T) {
	r := fixture()
	rhel, emr, gpu, tiny := x86("i-m4-rhel", "m4.large", "eu-west-1a"), x86("i-m4-emr", "m4.large", "eu-west-1a"), x86("i-g3", "g3.4xlarge", "eu-west-1a"), x86("i-c1", "c1.medium", "eu-west-1a")
	rhel.UsageOperation = aws.String("RunInstances:0010")
	emr.Tags = []ec2types.Tag{{Key: aws.String("aws:elasticmapreduce:job-flow-id"), Value: aws.String("j-1")}}
	r.Instances = []ec2types.Instance{x86("i-m4", "m4.large", "eu-west-1a"), rhel, emr, gpu, tiny}
	r.Offered = map[string]map[string]bool{"eu-west-1a": {"m7i.large": true, "m6a.large": true, "c7a.medium": true}}
	got := byResource(OldInstanceTypes(r, opts()))
	f := got["i-m4"]
	if f.Priced || !strings.Contains(f.Detail, "m4.large is an old generation ($0.1000 an hour, us-east-1 list prices): m7i.large, the usual pick, costs $0.0008 more an hour; m6a.large (AMD) costs $0.0864, less than now") {
		t.Errorf("m4.large: %+v", f)
	}
	for _, id := range []string{"i-m4-rhel", "i-g3", "i-c1"} {
		if d := got[id].Detail; strings.Contains(d, "$") || !strings.Contains(d, "compare their speed and price") {
			t.Errorf("%s: no price claim: %q", id, d)
		}
	}
	if _, ok := got["i-m4-emr"]; ok {
		t.Error("an EMR node is EMR's to change")
	}
	// The usual pick not offered in its zone: the cheapest one that is.
	r.Offered = map[string]map[string]bool{"eu-west-1a": {"m6a.large": true}}
	if d := byResource(OldInstanceTypes(r, opts()))["i-m4"].Detail; !strings.Contains(d, "m6a.large (AMD), the cheapest current type at least as big offered in its zone, costs $0.0864, less than now") {
		t.Errorf("no m7i in its zone: %q", d)
	}
}

// A group is one finding whatever its types: a type change mustn't read as a
// fix of the old type's finding.
func TestEC2GravitonGroupOfTypes(t *testing.T) {
	r := fixture()
	r.Instances = []ec2types.Instance{
		x86("i-g1", "m5.large", "eu-west-1a", "aws:autoscaling:groupName", "mixed"),
		x86("i-g2", "c5.large", "eu-west-1b", "aws:autoscaling:groupName", "mixed"),
	}
	r.Offered = map[string]map[string]bool{"eu-west-1a": {"m6g.large": true}, "eu-west-1b": {"c6g.large": true}}
	g := byResource(EC2Graviton(r, opts()))["mixed (Auto Scaling group)"]
	if g.Key != "asg/mixed" || g.Facts["from_type"] != "c5.large, m5.large" || g.Facts["to_type"] != "c6g.large, m6g.large" || g.Facts["instances"] != "2" {
		t.Fatalf("one finding for the group: %+v", g)
	}
	wantUSD(t, g, ((0.085-0.068)+(0.096-0.077))*730)
}

// An old type's instance storage: say when the current type has less.
func TestOldInstanceStorage(t *testing.T) {
	r := fixture()
	r.Instances = []ec2types.Instance{x86("i-c3", "c3.large", "eu-west-1a")}
	r.Offered = map[string]map[string]bool{"eu-west-1a": {"c6a.large": true}}
	if d := byResource(OldInstanceTypes(r, opts()))["i-c3"].Detail; !strings.Contains(d, "c6a.large has less instance storage than its 32 GB") {
		t.Errorf("c3.large has 2 x 16 GB of SSD: %q", d)
	}
}

// The groups that exist with no finding now, unless all on Graviton, are
// unjudged: their earlier finding is parked by the report.
func TestEC2GravitonUnjudged(t *testing.T) {
	r := fixture()
	spot := x86("i-spot1", "m5.large", "eu-west-1a", "aws:autoscaling:groupName", "spot-only")
	spot.InstanceLifecycle = ec2types.InstanceLifecycleTypeSpot
	arm := x86("i-arm1", "m7g.large", "eu-west-1a", "aws:autoscaling:groupName", "all-arm")
	arm.Architecture = ec2types.ArchitectureValuesArm64
	r.Instances = append(r.Instances, spot, arm)
	r.ASGNames = []string{"web-x86", "zeroed", "all-arm", "spot-only"}
	EC2Graviton(r, opts())
	if strings.Join(r.Unjudged, " ") != "ec2-graviton|asg/zeroed ec2-graviton|asg/spot-only" {
		t.Errorf("unjudged: %v", r.Unjudged)
	}
	r = fixture()
	r.ASGNames, r.ASGUnknown = []string{"zeroed"}, true
	EC2Graviton(r, opts())
	if len(r.Unjudged) != 0 {
		t.Errorf("groups unread: nothing claimed: %v", r.Unjudged)
	}
}
