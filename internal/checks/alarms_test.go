package checks

import (
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

func alarmRegion(now time.Time) *inventory.Region {
	old := now.AddDate(0, 0, -45)
	al := func(name, ns string, dims ...string) inventory.Alarm {
		a := inventory.Alarm{Name: name, ARN: "arn:aws:cloudwatch:eu-west-1:123456789012:alarm:" + name, Namespace: ns, Metric: "M",
			State: "INSUFFICIENT_DATA", Updated: old, Transitioned: old, Dimensions: map[string]string{}}
		for i := 0; i+1 < len(dims); i += 2 {
			a.Dimensions[dims[i]] = dims[i+1]
		}
		return a
	}
	fresh := al("fresh", "AWS/EC2", "InstanceId", "i-gone2")
	fresh.Updated = now.AddDate(0, 0, -3) // made again by a redeploy: counts from there
	transitioned := al("transitioned-lately", "AWS/EC2", "InstanceId", "i-gone3")
	transitioned.Transitioned = now.AddDate(0, 0, -10)
	ok := al("has-data", "AWS/EC2", "InstanceId", "i-gone4")
	ok.State = "OK"
	hr := al("fast cpu", "AWS/EC2", "InstanceId", "i-gone5")
	hr.HighRes = true
	math, comp, scaled := al("math", "AWS/EC2", "InstanceId", "i-gone6"), al("in-composite", "AWS/EC2", "InstanceId", "i-gone7"), al("scaled", "AWS/EC2", "AutoScalingGroupName", "old-asg")
	math.Math, comp.InComposite, scaled.Scaling = true, true, true
	return &inventory.Region{Name: "eu-west-1", AlarmsListed: true,
		Alarms: []inventory.Alarm{
			al("cpu i-gone", "AWS/EC2", "InstanceId", "i-gone"),
			al("cpu live", "AWS/EC2", "InstanceId", "i-live"),
			al("orders reads", "AWS/DynamoDB", "TableName", "orders-old", "GlobalSecondaryIndexName", "by-user"),
			al("lb 5xx", "AWS/ApplicationELB", "LoadBalancer", "app/web/50dc6c495c0c9188", "TargetGroup", "targetgroup/web/1"),
			al("fn errors", "AWS/Lambda", "FunctionName", "gone-fn"), // functions weren't read in full
			al("custom", "MyApp", "InstanceId", "i-gone"),
			al("agent mem", "CWAgent", "InstanceId", "i-gone"),
			al("tg only", "AWS/ApplicationELB", "TargetGroup", "targetgroup/web/1"),
			fresh, transitioned, ok, hr, math, comp, scaled,
		},
		Exists: map[string]map[string]bool{"InstanceId": {"i-live": true}, "TableName": {"orders": true}, "LoadBalancer": {},
			"AutoScalingGroupName": {}},
	}
}

func TestOrphanAlarms(t *testing.T) {
	o := opts()
	r := alarmRegion(o.Now)
	now := o.Now
	got := map[string]Finding{}
	for _, f := range OrphanAlarms(r, o) {
		got[f.Key] = f
	}
	want := []string{"cpu i-gone", "fast cpu", "lb 5xx", "orders reads"}
	if len(got) != len(want) {
		t.Fatalf("orphans: %v", keysOf(got))
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q (got %v)", k, keysOf(got))
		}
	}
	f := got["cpu i-gone"]
	if f.Confidence != "high" || f.MonthlyUSD != pricing.CWAlarmMonth || !f.Priced || f.Region != "eu-west-1" ||
		!strings.Contains(f.Detail, "the first 10 alarms in the account are free") || !strings.Contains(f.Detail, "no data for 45 days") {
		t.Errorf("an instance's alarm: %+v", f)
	}
	if f.Facts["alarm"] != "cpu i-gone" || f.Facts["dimension"] != "InstanceId" || f.Facts["resource_id"] != "i-gone" || f.Facts["resolution"] != "standard" ||
		f.Facts["state_updated"] != now.AddDate(0, 0, -45).Format("2006-01-02T15:04:05") {
		t.Errorf("facts: %v", f.Facts)
	}
	if len(f.Fix) != 2 || f.Fix[1] != "aws cloudwatch delete-alarms --region eu-west-1 --alarm-names 'cpu i-gone'" {
		t.Errorf("by ID: backup, then delete, the name quoted: %q", f.Fix)
	}
	if hr := got["fast cpu"]; hr.MonthlyUSD != pricing.CWHighResAlarmMonth || hr.Facts["resolution"] != "high" || !strings.HasPrefix(hr.Detail, "high-resolution") {
		t.Errorf("high resolution: %+v", hr)
	}
	if tb := got["orders reads"]; tb.Confidence != "medium" || !strings.Contains(tb.Why, "same name") || !strings.HasPrefix(tb.Fix[len(tb.Fix)-1], "# ") {
		t.Errorf("by name: medium, by hand: %+v", tb)
	}
	if lb := got["lb 5xx"]; lb.Confidence != "high" || lb.Facts["dimension"] != "LoadBalancer" {
		t.Errorf("a load balancer, by its ARN's end: %+v", lb)
	}
	// Every alarm that exists without a finding is unjudged.
	if n := strings.Count(strings.Join(r.Unjudged, "\n"), "orphan-alarm|"); n != len(r.Alarms)-len(want) {
		t.Errorf("unjudged: %v", r.Unjudged)
	}
	r = alarmRegion(now)
	r.AlarmsListed = false
	if fs := OrphanAlarms(r, o); fs != nil || r.Unjudged != nil {
		t.Errorf("alarms unread: nothing judged, nothing parked: %v %v", fs, r.Unjudged)
	}
}

// k orphans of N standard alarms: the first max(0, 10-(N-k)) cost nothing to keep.
func TestAlarmFreeTier(t *testing.T) {
	orphan := func(region, key, res string, usd float64) Finding {
		return Finding{Check: "orphan-alarm", Region: region, Key: key, MonthlyUSD: usd, Priced: true,
			Detail: "standard-resolution alarm on instance i-1, which is gone: no data for 40 days, " + alarmPriced(usd), Facts: map[string]string{"resolution": res}}
	}
	fs := []Finding{orphan("us-east-1", "b", "standard", 0.10), orphan("eu-west-1", "z", "standard", 0.10), orphan("eu-west-1", "a", "high", 0.30),
		orphan("us-east-1", "a", "standard", 0.10), {Check: "unattached-ebs", MonthlyUSD: 8, Priced: true}}
	AlarmFreeTier(fs, 11) // 3 standard orphans, 8 other standard alarms: 2 free
	if fs[1].MonthlyUSD != 0 || fs[3].MonthlyUSD != 0 || fs[0].MonthlyUSD != 0.10 || fs[2].MonthlyUSD != 0.30 || fs[4].MonthlyUSD != 8 {
		t.Fatalf("eu-west-1/z and us-east-1/a are free, in region and name order: %+v", fs)
	}
	if !fs[1].Priced || !fs[3].Priced {
		t.Error("a free one saves $0, priced: -min-usd hides it like the billed ones, instead of showing only the free ones")
	}
	if !strings.Contains(fs[1].Detail, "inside the account's 10 free alarms (11 standard alarms") || strings.Contains(fs[1].Detail, "bills $") {
		t.Errorf("detail: %q", fs[1].Detail)
	}
	fs = []Finding{orphan("us-east-1", "a", "standard", 0.10)}
	if AlarmFreeTier(fs, 40); fs[0].MonthlyUSD != 0.10 {
		t.Error("39 alarms besides it: it costs $0.10")
	}
}

func keysOf(m map[string]Finding) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A Lambda@Edge replica's metrics name "us-east-1.<function>", which no
// regional ListFunctions lists: never judged gone.
func TestOrphanAlarmsSkipEdgeReplicas(t *testing.T) {
	o := opts()
	old := o.Now.AddDate(0, 0, -60)
	al := func(name, fn string) inventory.Alarm {
		return inventory.Alarm{Name: name, Namespace: "AWS/Lambda", State: "INSUFFICIENT_DATA", Updated: old, Transitioned: old,
			Dimensions: map[string]string{"FunctionName": fn}}
	}
	r := &inventory.Region{Name: "eu-west-1", AlarmsListed: true, Alarms: []inventory.Alarm{al("edge", "us-east-1.edge-auth"), al("gone", "gone-fn")},
		Exists: map[string]map[string]bool{"FunctionName": {"live-fn": true}}}
	fs := OrphanAlarms(r, o)
	if len(fs) != 1 || fs[0].Key != "gone" || fs[0].Confidence != "medium" {
		t.Errorf("only the regional function that's gone: %+v", fs)
	}
}

// An orphan alarm a Route 53 health check takes its status from is left
// out and parked: deleting it can flip DNS failover.
func TestHealthCheckAlarms(t *testing.T) {
	orphan := func(key string) Finding {
		return Finding{Check: "orphan-alarm", Region: "eu-west-1", Key: key, Confidence: "high", Why: "gone"}
	}
	fs := []Finding{orphan("cpu high"), orphan("other"), {Check: "unattached-ebs", Region: "eu-west-1", Key: "vol-1"}}
	g := &inventory.Region{Name: "global", HealthChecks: []inventory.HealthCheck{{ID: "hc-1", Alarm: "eu-west-1|cpu high"}, {ID: "hc-2", Alarm: "us-east-1|other"}}}
	kept, parked := HealthCheckAlarms(fs, g)
	if len(kept) != 2 || kept[0].Key != "other" || kept[0].Confidence != "high" || strings.Join(parked, " ") != "|eu-west-1|orphan-alarm|cpu high" {
		t.Errorf("kept %+v parked %v", kept, parked)
	}
	g = &inventory.Region{Name: "global", Skipped: []inventory.Skip{{Region: "global", What: "route53:ListHealthChecks"}}}
	if kept, _ := HealthCheckAlarms(fs, g); kept[0].Confidence != "low" || !strings.HasPrefix(kept[0].Why, "the Route 53 health checks couldn't be read") {
		t.Errorf("health checks unread: low: %+v", kept[0])
	}
}
