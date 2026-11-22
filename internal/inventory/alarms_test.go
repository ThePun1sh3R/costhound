package inventory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/smithy-go"
)

type regionAlarms struct {
	fail  bool
	asked []cwtypes.AlarmType
}

func (f *regionAlarms) DescribeAlarms(_ context.Context, in *cloudwatch.DescribeAlarmsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error) {
	if f.fail {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	f.asked = in.AlarmTypes
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	alarm := func(name, ns string, period int32, dims ...string) cwtypes.MetricAlarm {
		a := cwtypes.MetricAlarm{AlarmName: aws.String(name), AlarmArn: aws.String("arn:aws:cloudwatch:eu-west-1:111111111111:alarm:" + name),
			Namespace: aws.String(ns), MetricName: aws.String("CPUUtilization"), Period: aws.Int32(period), StateValue: cwtypes.StateValueInsufficientData,
			StateUpdatedTimestamp: aws.Time(at)}
		for i := 0; i+1 < len(dims); i += 2 {
			a.Dimensions = append(a.Dimensions, cwtypes.Dimension{Name: aws.String(dims[i]), Value: aws.String(dims[i+1])})
		}
		return a
	}
	if in.NextToken == nil {
		scaled := alarm("scaled-by-policy", "AWS/EC2", 60, "AutoScalingGroupName", "web")
		scaled.AlarmActions = []string{"arn:aws:autoscaling:eu-west-1:111111111111:scalingPolicy:abc:autoScalingGroupName/web:policyName/up"}
		math := alarm("math", "", 60)
		math.Metrics = []cwtypes.MetricDataQuery{{Id: aws.String("m1")}}
		return &cloudwatch.DescribeAlarmsOutput{NextToken: aws.String("p2"), MetricAlarms: []cwtypes.MetricAlarm{
			alarm("cpu i-gone", "AWS/EC2", 60, "InstanceId", "i-gone"), alarm("fast", "AWS/EC2", 10, "InstanceId", "i-1"), scaled, math,
			alarm("TargetTracking-table/orders-AlarmHigh", "AWS/DynamoDB", 60, "TableName", "orders"),
		}}, nil
	}
	return &cloudwatch.DescribeAlarmsOutput{
		MetricAlarms:    []cwtypes.MetricAlarm{alarm("in-a-rule", "AWS/EBS", 60, "VolumeId", "vol-x"), alarm("suppressor", "AWS/EBS", 60, "VolumeId", "vol-y")},
		CompositeAlarms: []cwtypes.CompositeAlarm{{AlarmRule: aws.String(`ALARM("in-a-rule") OR ALARM(other)`), ActionsSuppressor: aws.String("suppressor")}},
	}, nil
}

func TestLoadAlarms(t *testing.T) {
	c := &regionAlarms{}
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}, Alarms: c})
	if !r.AlarmsListed || len(r.Alarms) != 7 || len(c.asked) != 2 {
		t.Fatalf("both pages, metric and composite alarms asked for: listed %v, %d alarms, asked %v", r.AlarmsListed, len(r.Alarms), c.asked)
	}
	got := map[string]Alarm{}
	for _, a := range r.Alarms {
		got[a.Name] = a
	}
	if a := got["cpu i-gone"]; a.Dimensions["InstanceId"] != "i-gone" || a.Namespace != "AWS/EC2" || a.State != "INSUFFICIENT_DATA" || a.Updated.IsZero() || a.HighRes || a.Math || a.Scaling || a.InComposite {
		t.Errorf("a plain one: %+v", a)
	}
	if !got["fast"].HighRes || !got["math"].Math || !got["scaled-by-policy"].Scaling || !got["TargetTracking-table/orders-AlarmHigh"].Scaling {
		t.Errorf("high resolution, metric math, Auto Scaling's own: %+v", got)
	}
	if !got["in-a-rule"].InComposite || !got["suppressor"].InComposite {
		t.Errorf("named by a composite's rule or as its suppressor: %+v %+v", got["in-a-rule"], got["suppressor"])
	}
	// Standard single-metric: all but the high-resolution one and the math one.
	if n := r.StandardAlarms(); n != 5 {
		t.Errorf("standard alarms: %d", n)
	}

	r = Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}, Alarms: &regionAlarms{fail: true}})
	if r.AlarmsListed || r.Alarms != nil || !strings.Contains(skipsOf(r), "cloudwatch:DescribeAlarms") {
		t.Errorf("refused: not listed, a skip: %v %v", r.AlarmsListed, r.Skipped)
	}
}

// An orphan is judged only against a list that was read in full.
func TestLoadMarksWhatWasReadInFull(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}})
	if got := r.Exists["VolumeId"]; len(got) != 2 || !got["vol-1"] || !got["vol-2"] {
		t.Errorf("volumes, both pages: %v", got)
	}
	if got := r.Exists["InstanceId"]; len(got) != 3 || !got["i-3"] {
		t.Errorf("instances: %v", got)
	}
	if got, ok := r.Exists["NatGatewayId"]; !ok || len(got) != 0 {
		t.Errorf("no NAT gateways, read in full: %v %v", got, ok)
	}
	for _, dim := range []string{"LogGroupName", "FunctionName", "TableName", "StreamName", "CacheClusterId", "AutoScalingGroupName", "DBInstanceIdentifier", "LoadBalancerName"} {
		if _, ok := r.Exists[dim]; ok {
			t.Errorf("%s: refused or never asked, so not read in full", dim)
		}
	}
	// The fake's load balancer ARNs have no "loadbalancer/" part to name them by.
	if got, ok := r.Exists["LoadBalancer"]; !ok || len(got) != 0 {
		t.Errorf("load balancers: %v %v", got, ok)
	}
	r = Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, ASG: deniedASG{}})
	if _, ok := r.Exists["AutoScalingGroupName"]; ok {
		t.Error("Auto Scaling groups refused: not read")
	}
	r = Load(context.Background(), "eu-west-1", "", Clients{EC2: &fakeEC2{}, ASG: fakeASG{}})
	if _, ok := r.Exists["AutoScalingGroupName"]; !ok || len(r.Exists["AutoScalingGroupName"]) != len(r.ASGNames) {
		t.Errorf("Auto Scaling groups read: %v", r.Exists["AutoScalingGroupName"])
	}
}

func skipsOf(r *Region) string {
	var s []string
	for _, k := range r.Skipped {
		s = append(s, k.What)
	}
	return strings.Join(s, " ")
}
