package inventory

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// AlarmsAPI reads a region's CloudWatch alarms (orphan-alarm).
type AlarmsAPI = cloudwatch.DescribeAlarmsAPIClient

// Alarm is what orphan-alarm keeps of a metric alarm: never its
// description, its state reason or where its actions send.
type Alarm struct {
	Name, ARN    string
	Namespace    string
	Metric       string
	Dimensions   map[string]string
	HighRes      bool        // a period under 60 seconds: billed as a high-resolution alarm
	Period       int         // seconds
	State        string      // OK, ALARM or INSUFFICIENT_DATA
	Updated      time.Time   // StateUpdatedTimestamp
	Transitioned time.Time   // StateTransitionedTimestamp (zero when AWS doesn't say)
	Math         bool        // metric math, anomaly detection or another account's metric (Metrics), or a PromQL query
	Scaling      bool        // an action names an Auto Scaling policy, or it's named TargetTracking-*: its service deletes it with the resource
	InComposite  bool        // a composite alarm's rule or actions suppressor names it
	Stats        []AlarmStat // a metric-math alarm's metrics (Math): what each one reads
}

// AlarmStat is one metric a metric-math alarm reads.
type AlarmStat struct {
	Namespace  string
	Dimensions map[string]string
	Period     int // seconds
}

// loadAlarms reads the region's metric alarms, and its composite alarms
// only to know which metric alarms they name.
func loadAlarms(ctx context.Context, r *Region, c AlarmsAPI, skip func(string, error)) {
	var alarms []Alarm
	var rules []string
	p := cloudwatch.NewDescribeAlarmsPaginator(c, &cloudwatch.DescribeAlarmsInput{
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *cloudwatch.DescribeAlarmsOutput) {
		for _, a := range o.MetricAlarms {
			name := aws.ToString(a.AlarmName)
			al := Alarm{Name: name, ARN: aws.ToString(a.AlarmArn), Namespace: aws.ToString(a.Namespace), Metric: aws.ToString(a.MetricName),
				State: string(a.StateValue), HighRes: aws.ToInt32(a.Period) > 0 && aws.ToInt32(a.Period) < 60, Period: int(aws.ToInt32(a.Period)),
				Math: len(a.Metrics) > 0 || a.EvaluationCriteria != nil, Scaling: strings.HasPrefix(name, "TargetTracking-")}
			if a.StateUpdatedTimestamp != nil {
				al.Updated = *a.StateUpdatedTimestamp
			}
			if a.StateTransitionedTimestamp != nil {
				al.Transitioned = *a.StateTransitionedTimestamp
			}
			for _, d := range a.Dimensions {
				if al.Dimensions == nil {
					al.Dimensions = map[string]string{}
				}
				al.Dimensions[aws.ToString(d.Name)] = aws.ToString(d.Value)
			}
			for _, m := range a.Metrics {
				if m.MetricStat == nil || m.MetricStat.Metric == nil {
					continue
				}
				st := AlarmStat{Namespace: aws.ToString(m.MetricStat.Metric.Namespace), Period: int(aws.ToInt32(m.MetricStat.Period)), Dimensions: map[string]string{}}
				for _, d := range m.MetricStat.Metric.Dimensions {
					st.Dimensions[aws.ToString(d.Name)] = aws.ToString(d.Value)
				}
				al.Stats = append(al.Stats, st)
			}
			for _, acts := range [][]string{a.AlarmActions, a.OKActions, a.InsufficientDataActions} {
				for _, act := range acts {
					// EC2 Auto Scaling's and Application Auto Scaling's policies alike.
					if strings.Contains(act, ":scalingPolicy:") {
						al.Scaling = true
					}
				}
			}
			alarms = append(alarms, al)
		}
		for _, ca := range o.CompositeAlarms {
			rules = append(rules, aws.ToString(ca.AlarmRule), aws.ToString(ca.ActionsSuppressor))
		}
	}); err != nil {
		skip("cloudwatch:DescribeAlarms", err)
		return
	}
	// A rule names an alarm by name or ARN; a name inside a longer one
	// counts too (on the safe side).
	for i := range alarms {
		for _, rule := range rules {
			if rule != "" && (strings.Contains(rule, alarms[i].Name) || alarms[i].ARN != "" && strings.Contains(rule, alarms[i].ARN)) {
				alarms[i].InComposite = true
				break
			}
		}
	}
	r.Alarms, r.AlarmsListed = alarms, true
}

// StandardAlarms counts the region's standard-resolution alarms on a single
// metric, which the account's 10 free alarms are taken from. Metric math and
// regions not scanned use some too, so it can only undercount, which keeps
// the orphans' saving on the low side.
func (r *Region) StandardAlarms() int {
	n := 0
	for _, a := range r.Alarms {
		if !a.Math && !a.HighRes {
			n++
		}
	}
	return n
}

// exists records a resource type read in full, under the metric dimension
// that names it in CloudWatch ("InstanceId"): orphan-alarm calls a resource
// gone only against a complete list.
func (r *Region) exists(dim string, names []string) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	if r.Exists == nil {
		r.Exists = map[string]map[string]bool{}
	}
	r.Exists[dim] = set
}

// liveIDs lists the IDs of the items that still exist.
func liveIDs[T any](items []T, id func(T) (string, bool)) []string {
	var out []string
	for _, it := range items {
		if v, live := id(it); live && v != "" {
			out = append(out, v)
		}
	}
	return out
}
