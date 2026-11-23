package checks

import (
	"slices"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func withMonitoring(in ec2types.Instance, categories ...string) ec2types.Instance {
	in.Monitoring = &ec2types.Monitoring{State: ec2types.MonitoringStateEnabled, MetricCategoryIds: categories}
	return in
}

func inGroup(in ec2types.Instance, asg string, more ...ec2types.Tag) ec2types.Instance {
	in.Tags = append([]ec2types.Tag{{Key: aws.String("aws:autoscaling:groupName"), Value: aws.String(asg)}}, more...)
	return in
}

func tagged(in ec2types.Instance, key, value string) ec2types.Instance {
	in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String(key), Value: aws.String(value)})
	return in
}

func TestDetailedMonitoring(t *testing.T) {
	run := ec2types.InstanceStateNameRunning
	r := &inventory.Region{Name: "eu-west-1",
		Instances: []ec2types.Instance{
			withMonitoring(inst("i-alone", "m5.large", run, "")),
			withMonitoring(inst("i-stopped", "m5.large", ec2types.InstanceStateNameStopped, "")),
			inst("i-basic", "m5.large", run, ""),
			inGroup(withMonitoring(inst("i-web-1", "m5.large", run, "")), "web"),
			inGroup(withMonitoring(inst("i-web-2", "m5.large", run, "")), "web"),
			inGroup(withMonitoring(inst("i-eks-1", "m5.large", run, "")), "eks-ng", ec2types.Tag{Key: aws.String("eks:nodegroup-name"), Value: aws.String("ng-1")}),
			withMonitoring(inst("i-watched", "m5.large", run, "")),
			withMonitoring(inst("i-two-min", "m5.large", run, "")),
			withMonitoring(inst("i-math", "m5.large", run, "")),
			withMonitoring(inst("i-five-min", "m5.large", run, "")),
			withMonitoring(inst("i-cats", "m5.large", run, ""), "cpu-10s"),
			tagged(withMonitoring(inst("i-kp-1", "m5.large", run, "")), "karpenter.sh/nodepool", "default"),
			tagged(withMonitoring(inst("i-kp-2", "m5.large", run, "")), "karpenter.sh/nodepool", "default"),
			tagged(withMonitoring(inst("i-sf-1", "m5.large", run, "")), "aws:ec2spot:fleet-request-id", "sfr-0123"),
			inGroup(withMonitoring(inst("i-lc-1", "m5.large", run, "")), "old-lc"),
			withMonitoring(inst("i-by-type", "c5.large", run, "")),
			func() ec2types.Instance {
				in := withMonitoring(inst("i-by-ami", "m5.large", run, ""))
				in.ImageId = aws.String("ami-0aggregate")
				return in
			}(),
		},
		ASGNames:  []string{"web", "eks-ng", "scaled-to-zero", "old-lc"},
		ASGLaunch: map[string]inventory.LaunchSource{"web": {Template: "web", TemplateID: "lt-0abc", Version: "$Default"}, "old-lc": {Config: "web-lc"}},
		Alarms: []inventory.Alarm{
			{Name: "web-scale-out", Namespace: "AWS/EC2", Period: 300, Scaling: true, Dimensions: map[string]string{"AutoScalingGroupName": "eks-ng"}},
			{Name: "cpu-1m", Namespace: "AWS/EC2", Period: 60, Dimensions: map[string]string{"InstanceId": "i-watched"}},
			{Name: "cpu-2m", Namespace: "AWS/EC2", Period: 120, Dimensions: map[string]string{"InstanceId": "i-two-min"}},
			{Name: "cpu-math", Math: true, Stats: []inventory.AlarmStat{{Namespace: "AWS/EC2", Period: 60, Dimensions: map[string]string{"InstanceId": "i-math"}}}},
			{Name: "cpu-5m", Namespace: "AWS/EC2", Period: 300, Dimensions: map[string]string{"InstanceId": "i-five-min"}},
			{Name: "c5-fleet-cpu", Namespace: "AWS/EC2", Period: 300, Dimensions: map[string]string{"InstanceType": "c5.large"}},
			{Name: "ami-cpu", Namespace: "AWS/EC2", Period: 3600, Dimensions: map[string]string{"ImageId": "ami-0aggregate"}},
		},
	}
	got := map[string]Finding{}
	for _, f := range DetailedMonitoring(r, opts()) {
		got[f.Key] = f
	}
	if len(got) != 13 {
		t.Fatalf("findings: %v", keysOf(got))
	}
	a := got["i-alone"]
	if !near(a.MonthlyUSD, 0.70) || a.Confidence != "medium" || a.Fix[0] != "aws ec2 unmonitor-instances --region eu-west-1 --instance-ids i-alone" ||
		!strings.Contains(a.Detail, "$0.10 a metric (the price past 10,000 metrics in the bill; up to $0.30 below that") || a.Facts["metrics"] != "7" || a.Facts["minute_alarm"] != "" {
		t.Errorf("a lone instance: %+v", a)
	}
	w := got["asg/web"]
	if len(w.Members) != 2 || !near(w.MonthlyUSD, 1.40) || !strings.Contains(strings.Join(w.Fix, " "), "launch template web (lt-0abc), version $Default") || w.Facts["sample"] != "i-web-1" ||
		w.Facts["launch_template"] != "web" || w.Facts["launch_template_id"] != "lt-0abc" || w.Facts["launch_version"] != "$Default" || w.Facts["launch"] != "" || strings.Contains(w.Detail, "web") ||
		!strings.Contains(w.Detail, "of one Auto Scaling group") {
		t.Errorf("a group: one finding, its launch template in the fix and in facts of their own, no name in the Detail: %+v", w)
	}
	if e := got["asg/eks-ng"]; e.Confidence != "low" || !strings.Contains(e.Fix[0], "EKS managed node group ng-1") || !strings.Contains(e.Why, "scales on alarms") {
		t.Errorf("an EKS node group that scales: %+v", e)
	}
	for id, alarm := range map[string]string{"i-watched": "cpu-1m", "i-two-min": "cpu-2m", "i-math": "cpu-math"} {
		if x := got[id]; x.Confidence != "low" || !strings.HasPrefix(x.Fix[0], "# ") || !strings.Contains(x.Why, "under 5 minutes") || x.Facts["minute_alarm"] != alarm {
			t.Errorf("%s: an alarm under 5 minutes (%s) watches it: %+v", id, alarm, x)
		}
	}
	if x := got["i-five-min"]; x.Confidence != "medium" {
		t.Errorf("a 5-minute alarm still has its data: %+v", x)
	}
	// The InstanceType and ImageId aggregates exist only with detailed monitoring: any period counts.
	for id, alarm := range map[string]string{"i-by-type": "c5-fleet-cpu", "i-by-ami": "ami-cpu"} {
		if x := got[id]; x.Confidence != "low" || x.Facts["minute_alarm"] != alarm || !strings.Contains(x.Why, "InstanceType or ImageId aggregate") {
			t.Errorf("%s: alarm %s on its aggregate: %+v", id, alarm, x)
		}
	}
	// A launcher that replaces its instances: one finding, fixed in its setting, never a runnable command.
	k := got["karpenter/default"]
	if len(k.Members) != 2 || k.Resource != "default (Karpenter node pool)" || k.Facts["launched_by"] != "karpenter" || k.Facts["launcher"] != "karpenter" || k.Facts["group"] != "default" ||
		!near(k.MonthlyUSD, 1.40) || len(k.Fix) != 1 || !strings.Contains(k.Fix[0], "spec.detailedMonitoring: false in its EC2NodeClass") || !strings.HasPrefix(k.Fix[0], "# ") {
		t.Errorf("a Karpenter node pool: %+v", k)
	}
	if w := got["asg/web"]; w.Facts["launcher"] != "asg" {
		t.Errorf("a group finding names its launcher kind: %+v", w)
	}
	if s := got["spot-fleet/sfr-0123"]; s.Resource != "sfr-0123 (Spot Fleet)" || !strings.Contains(s.Fix[0], "launch specification") {
		t.Errorf("a Spot Fleet: %+v", s)
	}
	if l := got["asg/old-lc"]; !strings.Contains(l.Fix[0], "launch configuration web-lc (it can't be changed: move the group to a launch template") || l.Facts["launch_configuration"] != "web-lc" {
		t.Errorf("a launch configuration can't change: %+v", l)
	}
	if c := got["i-cats"]; c.Confidence != "low" || !strings.Contains(c.Why, "cpu-10s") || c.Facts["categories"] != "cpu-10s" {
		t.Errorf("metric categories: %+v", c)
	}
	if u := strings.Join(r.Unjudged, " "); u != "ec2-detailed-monitoring|asg/scaled-to-zero" {
		t.Errorf("a group with no running instance: parked: %q", u)
	}
	// The alarms couldn't be read: one may watch it.
	r.Unjudged, r.Skipped = nil, []inventory.Skip{{Region: "eu-west-1", What: "cloudwatch:DescribeAlarms"}}
	for _, f := range DetailedMonitoring(r, opts()) {
		if f.Key == "i-alone" && (f.Confidence != "low" || !strings.HasPrefix(f.Fix[0], "# ") || f.Facts["alarms_unread"] != "yes") {
			t.Errorf("unread alarms: low, the command commented: %+v", f)
		}
	}
}

// A listed launcher with no running instance is unjudged (its setting may
// turn monitoring on at the next scale-out). One with a running unmonitored
// instance, or not listed, is not: the real fix, or deleted.
func TestDetailedMonitoringLauncherParked(t *testing.T) {
	run := ec2types.InstanceStateNameRunning
	arn := "arn:aws:batch:eu-west-1:111111111111:compute-environment/jobs"
	r := &inventory.Region{Name: "eu-west-1",
		ASGNames: []string{"scaled-to-zero", "still-running"}, // gone-from-list: deleted, not listed
		EC2Fleets: []inventory.EC2Fleet{
			{ID: "fleet-zero", State: "active", Type: "maintain"},
			{ID: "fleet-instant", State: "active", Type: "instant"},
			{ID: "fleet-running", State: "active", Type: "maintain"},
			{ID: "fleet-deleted", State: "deleted", Type: "maintain"},
		},
		SpotFleets: []inventory.SpotFleet{
			{ID: "sfr-zero", State: "active"},
			{ID: "sfr-running", State: "modifying"},
			{ID: "sfr-cancelled", State: "cancelled"},
		},
		BatchCEs: []inventory.BatchCE{
			{Name: "jobs", ARN: arn, Type: "MANAGED", State: "ENABLED", Status: "VALID", ResourceType: "EC2"},
			{Name: "spot-jobs", ARN: "arn:aws:batch:eu-west-1:111111111111:compute-environment/spot-jobs", Type: "MANAGED", State: "DISABLED", Status: "VALID", ResourceType: "SPOT"},
			{Name: "fargate", ARN: "arn:aws:batch:eu-west-1:111111111111:compute-environment/fargate", Type: "MANAGED", State: "ENABLED", Status: "VALID", ResourceType: "FARGATE"},
			{Name: "deleted", ARN: "arn:aws:batch:eu-west-1:111111111111:compute-environment/deleted", Type: "MANAGED", State: "ENABLED", Status: "DELETED", ResourceType: "EC2"},
			{Name: "busy", ARN: "arn:aws:batch:eu-west-1:111111111111:compute-environment/busy", Type: "MANAGED", State: "ENABLED", Status: "VALID", ResourceType: "EC2"},
		},
		Instances: []ec2types.Instance{
			inGroup(inst("i-asg-basic", "m5.large", run, ""), "still-running"), // unmonitored: the fix
			tagged(inst("i-fleet-basic", "m5.large", run, ""), "aws:ec2:fleet-id", "fleet-running"),
			tagged(inst("i-sfr-basic", "m5.large", run, ""), "aws:ec2spot:fleet-request-id", "sfr-running"),
			tagged(inst("i-batch-basic", "m5.large", run, ""), "aws:batch:compute-environment", "busy"),
			func() ec2types.Instance {
				in := tagged(withMonitoring(inst("i-kp", "m5.large", run, "")), "karpenter.sh/nodepool", "default")
				in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String("kubernetes.io/cluster/prod"), Value: aws.String("owned")})
				return in
			}(),
		},
	}
	_ = DetailedMonitoring(r, opts())
	got := strings.Join(r.Unjudged, " ")
	for _, want := range []string{
		"ec2-detailed-monitoring|asg/scaled-to-zero",
		"ec2-detailed-monitoring|ec2-fleet/fleet-zero",
		"ec2-detailed-monitoring|spot-fleet/sfr-zero",
		"ec2-detailed-monitoring|batch/jobs",
		"ec2-detailed-monitoring|batch/" + arn,
		"ec2-detailed-monitoring|batch/spot-jobs",
		"ec2-detailed-monitoring|batch/arn:aws:batch:eu-west-1:111111111111:compute-environment/spot-jobs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want parked %q in %q", want, got)
		}
	}
	for _, no := range []string{
		"asg/still-running", "asg/gone-from-list",
		"ec2-fleet/fleet-instant", "ec2-fleet/fleet-running", "ec2-fleet/fleet-deleted",
		"spot-fleet/sfr-running", "spot-fleet/sfr-cancelled",
		"batch/fargate", "batch/deleted", "batch/busy",
	} {
		if strings.Contains(got, no) {
			t.Errorf("must not park %q in %q", no, got)
		}
	}
	fs := DetailedMonitoring(&inventory.Region{Name: "eu-west-1", Instances: r.Instances}, opts())
	var kp Finding
	for _, f := range fs {
		if f.Key == "karpenter/default" {
			kp = f
		}
	}
	if kp.Facts["eks_cluster"] != "prod" || kp.Facts["launcher"] != "karpenter" {
		t.Errorf("Karpenter carries eks_cluster from the tag key: %+v", kp)
	}
	// eks:eks-cluster-name when the kubernetes.io tag is absent.
	in := tagged(withMonitoring(inst("i-kp2", "m5.large", run, "")), "karpenter.sh/nodepool", "gpu")
	in.Tags = append(in.Tags, ec2types.Tag{Key: aws.String("eks:eks-cluster-name"), Value: aws.String("staging")})
	fs = DetailedMonitoring(&inventory.Region{Name: "eu-west-1", Instances: []ec2types.Instance{in}}, opts())
	if fs[0].Facts["eks_cluster"] != "staging" {
		t.Errorf("eks:eks-cluster-name: %+v", fs[0])
	}
}

// The account's first 10 metrics are free, counted once; a known count only
// lowers the price.
func TestMonitoringTier(t *testing.T) {
	run := ec2types.InstanceStateNameRunning
	price := func(instances int, custom int, known bool) []float64 {
		r := &inventory.Region{Name: "eu-west-1"}
		for i := range instances {
			r.Instances = append(r.Instances, withMonitoring(inst("i-"+string(rune('a'+i)), "m5.large", run, "")))
		}
		fs := DetailedMonitoring(r, opts())
		MonitoringTier(fs, custom, known)
		var out []float64
		for _, f := range fs {
			out = append(out, f.MonthlyUSD)
		}
		return out
	}
	for _, c := range []struct {
		instances, custom int
		known             bool
		want              []float64
	}{
		{1, 0, false, []float64{0}},       // unknown: these take the free 10
		{2, 0, false, []float64{0, 0.40}}, // 14 metrics, 10 free, 4 at $0.10
	} {
		got := price(c.instances, c.custom, c.known)
		for i := range c.want {
			if !near(got[i], c.want[i]) {
				t.Errorf("%d instance(s), %d custom metrics (known %v): %v, want %v", c.instances, c.custom, c.known, got, c.want)
				break
			}
		}
	}
	east := DetailedMonitoring(&inventory.Region{Name: "us-east-1", Instances: []ec2types.Instance{withMonitoring(inst("i-9", "m5.large", run, ""))}}, opts())
	west := DetailedMonitoring(&inventory.Region{Name: "eu-west-1", Instances: []ec2types.Instance{withMonitoring(inst("i-9", "m5.large", run, ""))}}, opts())
	for _, fs := range [][]Finding{append(slices.Clone(east), west...), append(slices.Clone(west), east...)} {
		MonitoringTier(fs, 0, false)
		for _, f := range fs {
			if want := map[string]float64{"eu-west-1": 0, "us-east-1": 0.40}[f.Region]; !near(f.MonthlyUSD, want) {
				t.Errorf("the free 10 go by region and key, whatever the order: %s $%.2f, want $%.2f", f.Region, f.MonthlyUSD, want)
			}
		}
	}
	r := &inventory.Region{Name: "eu-west-1", Instances: []ec2types.Instance{withMonitoring(inst("i-1", "m5.large", run, ""))}}
	fs := DetailedMonitoring(r, opts())
	MonitoringTier(fs, 0, false)
	if !strings.Contains(fs[0].Detail, "about $0.00 a month at $0.10 a metric") || !strings.Contains(fs[0].Detail, "7 of its metrics in the account's first 10, which are free") {
		t.Errorf("the Detail says what the free 10 took: %s", fs[0].Detail)
	}
}
