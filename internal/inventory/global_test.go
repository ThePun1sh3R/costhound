package inventory

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"
)

type fakeRoute53 struct {
	failRecords, stuck    bool
	recordCalls           []string
	zones                 []r53types.HostedZone // nil: Z1 and Z2, for the health checks
	failZones, failDNSSEC bool
	signed                map[string]bool
	dnssecCalls           []string
}

func (f *fakeRoute53) ListHealthChecks(context.Context, *route53.ListHealthChecksInput, ...func(*route53.Options)) (*route53.ListHealthChecksOutput, error) {
	hc := func(id string, typ r53types.HealthCheckType, cfg r53types.HealthCheckConfig) r53types.HealthCheck {
		cfg.Type = typ
		return r53types.HealthCheck{Id: aws.String(id), HealthCheckConfig: &cfg}
	}
	linked := hc("hc-linked", r53types.HealthCheckTypeHttp, r53types.HealthCheckConfig{})
	linked.LinkedService = &r53types.LinkedService{ServicePrincipal: aws.String("servicediscovery.amazonaws.com")}
	return &route53.ListHealthChecksOutput{HealthChecks: []r53types.HealthCheck{
		hc("hc-a", r53types.HealthCheckTypeHttps, r53types.HealthCheckConfig{FullyQualifiedDomainName: aws.String("api.example.com"), RequestInterval: aws.Int32(10)}),
		hc("hc-b", r53types.HealthCheckTypeHttpsStrMatch, r53types.HealthCheckConfig{IPAddress: aws.String("192.0.2.1"), MeasureLatency: aws.Bool(true)}),
		hc("hc-calc", r53types.HealthCheckTypeCalculated, r53types.HealthCheckConfig{ChildHealthChecks: []string{"hc-child"}}),
		hc("hc-child", r53types.HealthCheckTypeHttp, r53types.HealthCheckConfig{Disabled: aws.Bool(true)}),
		hc("hc-arc", r53types.HealthCheckTypeRecoveryControl, r53types.HealthCheckConfig{}),
		linked,
		hc("hc-alarmed", r53types.HealthCheckTypeTcp, r53types.HealthCheckConfig{}),
		hc("hc-math", r53types.HealthCheckTypeTcp, r53types.HealthCheckConfig{}),
		hc("hc-metric", r53types.HealthCheckTypeCloudwatchMetric, r53types.HealthCheckConfig{
			AlarmIdentifier: &r53types.AlarmIdentifier{Region: r53types.CloudWatchRegionEuWest1, Name: aws.String("cpu high")}}),
	}}, nil
}

func (f *fakeRoute53) ListHostedZones(context.Context, *route53.ListHostedZonesInput, ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	if f.failZones {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	if f.zones != nil {
		return &route53.ListHostedZonesOutput{HostedZones: f.zones}, nil
	}
	return &route53.ListHostedZonesOutput{HostedZones: []r53types.HostedZone{{Id: aws.String("/hostedzone/Z1")}, {Id: aws.String("/hostedzone/Z2")}}}, nil
}

func (f *fakeRoute53) ListResourceRecordSets(_ context.Context, in *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	f.recordCalls = append(f.recordCalls, aws.ToString(in.HostedZoneId)+"@"+aws.ToString(in.StartRecordName)+"/"+string(in.StartRecordType))
	if f.failRecords && aws.ToString(in.HostedZoneId) == "/hostedzone/Z2" {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	if f.stuck { // a broken endpoint: always "more", always from the same record
		return &route53.ListResourceRecordSetsOutput{IsTruncated: true, NextRecordName: aws.String("a.example.com."), NextRecordType: r53types.RRTypeA}, nil
	}
	rec := func(name, hc string) r53types.ResourceRecordSet {
		rs := r53types.ResourceRecordSet{Name: aws.String(name), Type: r53types.RRTypeA, ResourceRecords: []r53types.ResourceRecord{{Value: aws.String("192.0.2.99")}}}
		if hc != "" {
			rs.HealthCheckId = aws.String(hc)
		}
		return rs
	}
	if aws.ToString(in.HostedZoneId) == "/hostedzone/Z1" && in.StartRecordName == nil { // two pages
		return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []r53types.ResourceRecordSet{rec("a.example.com.", "")},
			IsTruncated: true, NextRecordName: aws.String("b.example.com."), NextRecordType: r53types.RRTypeA}, nil
	}
	if aws.ToString(in.HostedZoneId) == "/hostedzone/Z1" {
		return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []r53types.ResourceRecordSet{rec("b.example.com.", "hc-a")}}, nil
	}
	return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: []r53types.ResourceRecordSet{rec("c.example.org.", "")}}, nil
}

func (f *fakeRoute53) ListTrafficPolicyInstances(context.Context, *route53.ListTrafficPolicyInstancesInput, ...func(*route53.Options)) (*route53.ListTrafficPolicyInstancesOutput, error) {
	return &route53.ListTrafficPolicyInstancesOutput{}, nil
}

func (f *fakeRoute53) GetDNSSEC(_ context.Context, in *route53.GetDNSSECInput, _ ...func(*route53.Options)) (*route53.GetDNSSECOutput, error) {
	id := aws.ToString(in.HostedZoneId)
	f.dnssecCalls = append(f.dnssecCalls, id)
	if f.failDNSSEC {
		return nil, &smithy.GenericAPIError{Code: "AccessDenied"}
	}
	if f.signed[id] {
		return &route53.GetDNSSECOutput{Status: &r53types.DNSSECStatus{ServeSignature: aws.String("SIGNING")},
			KeySigningKeys: []r53types.KeySigningKey{{Name: aws.String("ksk-1"), Status: aws.String("ACTIVE")}}}, nil
	}
	return &route53.GetDNSSECOutput{Status: &r53types.DNSSECStatus{ServeSignature: aws.String("NOT_SIGNING")}}, nil
}

type fakeAlarms struct{}

func (fakeAlarms) DescribeAlarms(context.Context, *cloudwatch.DescribeAlarmsInput, ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error) {
	dim := func(id string) []cwtypes.Dimension {
		return []cwtypes.Dimension{{Name: aws.String("HealthCheckId"), Value: aws.String(id)}}
	}
	return &cloudwatch.DescribeAlarmsOutput{MetricAlarms: []cwtypes.MetricAlarm{
		{Namespace: aws.String("AWS/Route53"), MetricName: aws.String("HealthCheckStatus"), Dimensions: dim("hc-alarmed")},
		{Metrics: []cwtypes.MetricDataQuery{{MetricStat: &cwtypes.MetricStat{Metric: &cwtypes.Metric{Namespace: aws.String("AWS/Route53"), Dimensions: dim("hc-math")}}}}},
		{Namespace: aws.String("AWS/EC2"), Dimensions: dim("hc-b")}, // not a Route 53 metric
	}}, nil
}

func TestLoadGlobalHealthChecks(t *testing.T) {
	r53 := &fakeRoute53{}
	r := LoadGlobal(context.Background(), GlobalClients{Route53: r53, Alarms: fakeAlarms{}})
	if r.Name != "global" || len(r.Skipped) != 0 || r.HealthCheckUsesUnknown || r.HealthCheckAlarmsUnknown || r.TrafficPolicies {
		t.Fatalf("region %q skips %v flags %v %v %v", r.Name, r.Skipped, r.HealthCheckUsesUnknown, r.HealthCheckAlarmsUnknown, r.TrafficPolicies)
	}
	got := map[string]HealthCheck{}
	for _, h := range r.HealthChecks {
		got[h.ID] = h
	}
	if h := got["hc-a"]; !h.Used || h.Options != 2 || h.Target != "api.example.com" {
		t.Errorf("hc-a: named by a record on the second page; HTTPS + fast interval: %+v", h)
	}
	if h := got["hc-b"]; h.Used || h.Watched || h.Options != 3 || h.Target != "192.0.2.1" {
		t.Errorf("hc-b: HTTPS, string matching, latency; an EC2 alarm doesn't count: %+v", h)
	}
	if !got["hc-child"].Used || !got["hc-child"].Disabled || got["hc-calc"].Used || !got["hc-arc"].Used || got["hc-linked"].Managed == "" {
		t.Errorf("a calculated check's child and an ARC check are used; linked: %+v", got)
	}
	if !got["hc-alarmed"].Watched || !got["hc-math"].Watched {
		t.Errorf("alarms, including metric math: %+v", got)
	}
	if got["hc-metric"].Alarm != "eu-west-1|cpu high" || got["hc-a"].Alarm != "" {
		t.Errorf("the alarm a CLOUDWATCH_METRIC check takes its status from: %+v", got["hc-metric"])
	}
	if strings.Join(r53.recordCalls, " ") != "/hostedzone/Z1@/ /hostedzone/Z1@b.example.com./A /hostedzone/Z2@/" {
		t.Errorf("record pages: %v", r53.recordCalls)
	}

	r = LoadGlobal(context.Background(), GlobalClients{Route53: &fakeRoute53{failRecords: true}, Alarms: fakeAlarms{}})
	if !r.HealthCheckUsesUnknown || len(r.Skipped) != 1 || r.Skipped[0].What != "route53:ListResourceRecordSets" || r.Skipped[0].Region != "global" {
		t.Errorf("an unread zone makes every health check unknown, plus a skip: %v %v", r.HealthCheckUsesUnknown, r.Skipped)
	}
	r = LoadGlobal(context.Background(), GlobalClients{Route53: &fakeRoute53{}})
	if !r.HealthCheckAlarmsUnknown {
		t.Error("no alarm client: alarms unknown")
	}
}

func TestRecordPagesThatDontMoveOnStop(t *testing.T) {
	r53 := &fakeRoute53{stuck: true}
	r := LoadGlobal(context.Background(), GlobalClients{Route53: r53, Alarms: fakeAlarms{}})
	if !r.HealthCheckUsesUnknown || len(r53.recordCalls) != 2 {
		t.Fatalf("a page that doesn't move on stops the walk, unread: %v %v", r.HealthCheckUsesUnknown, r53.recordCalls)
	}
}

func TestLoadZones(t *testing.T) {
	zone := func(id, name string, records int64, private bool) r53types.HostedZone {
		return r53types.HostedZone{Id: aws.String("/hostedzone/" + id), Name: aws.String(name), ResourceRecordSetCount: aws.Int64(records),
			Config: &r53types.HostedZoneConfig{PrivateZone: private}}
	}
	linked := zone("ZLINK", "svc.local.", 2, false)
	linked.LinkedService = &r53types.LinkedService{ServicePrincipal: aws.String("servicediscovery.amazonaws.com")}
	zones := []r53types.HostedZone{zone("ZEMPTY", "empty.example.com.", 2, false), zone("ZSIGNED", "signed.example.com.", 2, false),
		zone("ZPRIV", "corp.internal.", 2, true), linked, zone("ZFULL", "full.example.com.", 9, false)}
	r53 := &fakeRoute53{zones: zones, signed: map[string]bool{"ZSIGNED": true}}
	r := LoadGlobal(context.Background(), GlobalClients{Route53: r53, Alarms: fakeAlarms{}})
	got := map[string]HostedZone{}
	for _, z := range r.Zones {
		got[z.ID] = z
	}
	if !r.ZonesListed || len(got) != 5 || len(r.Skipped) != 0 {
		t.Fatalf("listed %v zones %v skips %v", r.ZonesListed, r.Zones, r.Skipped)
	}
	if z := got["ZEMPTY"]; z.Name != "empty.example.com" || z.Records != 2 || z.Private || z.Linked != "" || z.DNSSEC != "none" {
		t.Errorf("an empty public zone, not signed: %+v", z)
	}
	if got["ZSIGNED"].DNSSEC != "signed" || !got["ZPRIV"].Private || got["ZLINK"].Linked != "servicediscovery.amazonaws.com" {
		t.Errorf("signed, private, linked: %+v", got)
	}
	// GetDNSSEC only for the empty public zones no service owns.
	if strings.Join(r53.dnssecCalls, " ") != "ZEMPTY ZSIGNED" {
		t.Errorf("DNSSEC asked of: %v", r53.dnssecCalls)
	}

	r = LoadGlobal(context.Background(), GlobalClients{Route53: &fakeRoute53{zones: zones, failDNSSEC: true}, Alarms: fakeAlarms{}})
	if r.Zones[0].DNSSEC != "unknown" || r.Zones[1].DNSSEC != "unknown" || len(r.Skipped) != 1 || r.Skipped[0].What != "route53:GetDNSSEC" {
		t.Errorf("GetDNSSEC refused: unknown, one skip: %+v %v", r.Zones, r.Skipped)
	}
	// Unlisted: no zones, and no health check is known unused either.
	r = LoadGlobal(context.Background(), GlobalClients{Route53: &fakeRoute53{failZones: true}, Alarms: fakeAlarms{}})
	if r.ZonesListed || r.Zones != nil || !r.HealthCheckUsesUnknown || len(r.Skipped) != 1 || r.Skipped[0].What != "route53:ListHostedZones" {
		t.Errorf("ListHostedZones refused: %v %v %v %v", r.ZonesListed, r.Zones, r.HealthCheckUsesUnknown, r.Skipped)
	}
}
