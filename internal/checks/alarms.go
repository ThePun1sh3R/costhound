package checks

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// OrphanAlarmDays: an alarm with no data for this long, on a resource
// that's gone, is reported.
const OrphanAlarmDays = 30

// orphanDims are the dimensions an alarm's resource is judged by, per AWS
// namespace, in order. byID: an ID AWS never hands out again, so the alarm
// can't come back to life and its fix is scripted. A name can be used again
// (a restore, a failover, a redeploy), so those are medium and by hand.
// Target groups, EFS, Aurora clusters and custom or agent namespaces aren't
// judged.
var orphanDims = []struct {
	dim, what  string
	namespaces []string
	byID       bool
}{
	{"InstanceId", "instance", []string{"AWS/EC2"}, true},
	{"VolumeId", "volume", []string{"AWS/EBS"}, true},
	{"NatGatewayId", "NAT gateway", []string{"AWS/NATGateway"}, true},
	{"LoadBalancer", "load balancer", []string{"AWS/ApplicationELB", "AWS/NetworkELB", "AWS/GatewayELB"}, true},
	{"LoadBalancerName", "Classic Load Balancer", []string{"AWS/ELB"}, false},
	{"DBInstanceIdentifier", "DB instance", []string{"AWS/RDS"}, false},
	{"FunctionName", "Lambda function", []string{"AWS/Lambda"}, false},
	{"TableName", "DynamoDB table", []string{"AWS/DynamoDB"}, false},
	{"StreamName", "Kinesis stream", []string{"AWS/Kinesis"}, false},
	{"CacheClusterId", "ElastiCache cluster", []string{"AWS/ElastiCache"}, false},
	{"AutoScalingGroupName", "Auto Scaling group", []string{"AWS/EC2", "AWS/AutoScaling"}, false},
	{"LogGroupName", "log group", []string{"AWS/Logs"}, false},
}

// OrphanAlarms finds CloudWatch alarms on a resource that's gone: one
// metric of an AWS namespace, no data for 30 days (INSUFFICIENT_DATA since,
// by both state timestamps), and the resource its dimension names isn't in
// the region's complete list of that type. Each bills $0.10 a month, $0.30
// at high resolution (CW:AlarmMonitorUsage); AlarmFreeTier takes the
// account's 10 free ones off once the regions are joined. Left out: metric
// math, anomaly detection and PromQL alarms, composites and the alarms they
// name, and the alarms Auto Scaling makes and deletes itself. An alarm that
// exists without a finding is unjudged, so an earlier finding is parked,
// not fixed.
func OrphanAlarms(r *inventory.Region, o Options) []Finding {
	if !r.AlarmsListed {
		return nil
	}
	var out []Finding
	for _, a := range r.Alarms {
		if f, ok := orphanAlarm(r, o, a); ok {
			out = append(out, f)
		} else {
			r.Unjudged = append(r.Unjudged, "orphan-alarm|"+a.Name)
		}
	}
	return out
}

func orphanAlarm(r *inventory.Region, o Options, a inventory.Alarm) (Finding, bool) {
	if a.Math || a.InComposite || a.Scaling || a.State != "INSUFFICIENT_DATA" || a.Updated.IsZero() || !strings.HasPrefix(a.Namespace, "AWS/") {
		return Finding{}, false
	}
	since := a.Updated
	if a.Transitioned.After(since) {
		since = a.Transitioned
	}
	days := int(o.Now.Sub(since).Hours() / 24)
	if days < OrphanAlarmDays {
		return Finding{}, false // a redeploy that made it again starts the count again
	}
	for _, d := range orphanDims {
		v, has := a.Dimensions[d.dim]
		if !has || !slices.Contains(d.namespaces, a.Namespace) {
			continue
		}
		live, listed := r.Exists[d.dim]
		// A Lambda@Edge replica reports as "us-east-1.<function>", which no
		// regional ListFunctions lists (function names have no dots).
		if !listed || v == "" || live[v] || d.dim == "FunctionName" && strings.Contains(v, ".") {
			return Finding{}, false
		}
		usd, resolution, res := pb(r).AlarmMonth, "standard-resolution", "standard"
		if a.HighRes {
			usd, resolution, res = pb(r).HighResAlarmMonth, "high-resolution", "high"
		}
		f := Finding{
			Check: "orphan-alarm", Region: r.Name, Resource: a.Name, Key: a.Name, Confidence: "medium",
			MonthlyUSD: usd, Priced: true,
			Detail: fmt.Sprintf("%s alarm on %s %s, which is gone: no data for %d days, %s", resolution, d.what, v, days, alarmPriced(usd)),
			Why:    fmt.Sprintf("a %s with this name can come back (a restore, a failover, a redeploy under the same name) and the alarm would watch it again; %s", d.what, alarmUsers),
			Evidence: []string{fmt.Sprintf("CloudWatch DescribeAlarms: %s %s on %s=%s, INSUFFICIENT_DATA since %s; this region's complete list of %ss has no %s",
				a.Namespace, a.Metric, d.dim, v, since.UTC().Format("2006-01-02"), d.what, v)},
			Facts: facts("alarm", a.Name, "alarm_arn", a.ARN, "namespace", a.Namespace, "dimension", d.dim, "resource_id", v,
				"resolution", res, "state_updated", a.Updated.UTC().Format("2006-01-02T15:04:05"), "days", itoa(days)),
		}
		if d.byID {
			f.Confidence, f.Why = "high", fmt.Sprintf("the %s's ID is gone for good, so the alarm can't fire again (if it was kept as a reminder, its definition is in the fix's backup); %s", d.what, alarmUsers)
		}
		f.Fix = alarmFix(r.Name, a.Name, d.byID)
		return f, true
	}
	return Finding{}, false
}

// alarmUsers: what else can name an alarm, which costhound doesn't read.
const alarmUsers = "a CodeDeploy deployment group, an ECS deployment or a CloudFormation rollback trigger may still name it (CodeDeploy stops a deployment when it can't read one)"

func alarmPriced(usd float64) string {
	return fmt.Sprintf("but it bills $%.2f a month (the first 10 alarms in the account are free, at standard resolution only)", usd)
}

func alarmFix(region, name string, byID bool) []string {
	if strings.ContainsFunc(name, unicode.IsControl) {
		return []string{"# its name has control characters: delete it in the CloudWatch console"}
	}
	q := shellQuote(name)
	backup := "aws cloudwatch describe-alarms --region " + region + " --alarm-names " + q + " > alarm-backup.json"
	del := "aws cloudwatch delete-alarms --region " + region + " --alarm-names " + q
	if byID {
		return []string{backup, del}
	}
	return []string{"# make sure nothing with that name is coming back, then:", "# " + backup, "# " + del}
}

// AlarmFreeTier applies the account's 10 free standard-resolution alarms,
// once its regions are joined (standard: the standard single-metric alarms
// in all of them). Deleting k orphans saves only what the account pays for
// above the free 10, so the first max(0, 10-(standard-k)) of them, in a
// stable order, save $0: priced at nothing, so -min-usd treats them like the
// billed ones. High-resolution alarms are never free.
func AlarmFreeTier(fs []Finding, standard int) {
	var idx []int
	for i := range fs {
		if fs[i].Check == "orphan-alarm" && fs[i].Facts["resolution"] == "standard" {
			idx = append(idx, i)
		}
	}
	sort.Slice(idx, func(a, b int) bool {
		x, y := fs[idx[a]], fs[idx[b]]
		if x.Region != y.Region {
			return x.Region < y.Region
		}
		return x.Key < y.Key
	})
	free := min(len(idx), max(0, pricing.CWFreeAlarms-(standard-len(idx))))
	for _, i := range idx[:free] {
		f := &fs[i]
		f.Detail = strings.Replace(f.Detail, alarmPriced(f.MonthlyUSD), fmt.Sprintf(
			"inside the account's 10 free alarms (%d standard alarms in the scanned regions), so deleting it saves nothing until there are more", standard), 1)
		f.MonthlyUSD = 0
	}
}

// HealthCheckAlarms leaves out the orphan alarms a Route 53 health check
// takes its status from (CLOUDWATCH_METRIC): deleting one can flip DNS
// failover. They're parked, not fixed. When the health checks couldn't be
// read, the other orphans are low, with that reason. g is the global pass,
// which runs after the regions.
func HealthCheckAlarms(fs []Finding, g *inventory.Region) (kept []Finding, parked []string) {
	used, known := map[string]bool{}, true
	for _, sk := range g.Skipped {
		known = known && sk.What != "route53:ListHealthChecks"
	}
	for _, h := range g.HealthChecks {
		if h.Alarm != "" {
			used[h.Alarm] = true
		}
	}
	for _, f := range fs {
		if f.Check == "orphan-alarm" {
			if used[f.Region+"|"+f.Key] {
				parked = append(parked, "|"+f.Region+"|orphan-alarm|"+f.Key)
				continue
			}
			if !known {
				f.Confidence = "low"
				f.Why = "the Route 53 health checks couldn't be read, and one may take its status from this alarm (deleting it can flip DNS failover); " + f.Why
			}
		}
		kept = append(kept, f)
	}
	return kept, parked
}
