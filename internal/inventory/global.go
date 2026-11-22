package inventory

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// Route53API is the Route 53 calls the health check check uses.
type Route53API interface {
	route53.ListHealthChecksAPIClient
	route53.ListHostedZonesAPIClient
	ListResourceRecordSets(context.Context, *route53.ListResourceRecordSetsInput, ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ListTrafficPolicyInstances(context.Context, *route53.ListTrafficPolicyInstancesInput, ...func(*route53.Options)) (*route53.ListTrafficPolicyInstancesOutput, error)
	GetDNSSEC(context.Context, *route53.GetDNSSECInput, ...func(*route53.Options)) (*route53.GetDNSSECOutput, error)
}

// HostedZone is what empty-hosted-zone keeps of a hosted zone.
type HostedZone struct {
	ID, Name string // the ID without "/hostedzone/", the name without the final dot
	Private  bool
	Records  int64
	Linked   string // the AWS service that made it and owns it (LinkedService), "" if none
	DNSSEC   string // a public zone with only NS and SOA: "none", "signed" or "unknown"; "" unchecked
}

// GlobalClients are the clients of the account-wide services. Route 53 and
// its CloudWatch metrics answer in us-east-1.
type GlobalClients struct {
	Route53 Route53API
	Alarms  cloudwatch.DescribeAlarmsAPIClient
}

// HealthCheck is a Route 53 health check and what uses it. Record sets are
// read only for the health check IDs they name: nothing else of a record is
// kept.
type HealthCheck struct {
	ID, Type string
	Target   string // the domain name or IP address it probes ("" for calculated, metric and ARC checks)
	Options  int    // optional features billed on top: HTTPS, string matching, fast interval, latency
	Disabled bool
	Managed  string // the AWS service that made it and uses it (LinkedService), "" if none
	Used     bool   // a record set or a calculated health check names it, or it is an ARC routing control
	Watched  bool   // a CloudWatch alarm watches its metrics
	Alarm    string // CLOUDWATCH_METRIC: the alarm its status comes from, "<region>|<name>"
}

// LoadGlobal reads the account-wide resources the global checks judge, once
// per scan; their region is "global".
func LoadGlobal(ctx context.Context, c GlobalClients) *Region {
	r := &Region{Name: "global"}
	skip := func(what string, err error) {
		r.Skipped = append(r.Skipped, Skip{Region: "global", What: what, Error: ShortError(err)})
	}
	if c.Route53 != nil {
		loadZones(ctx, r, c, skip)
		loadHealthChecks(ctx, r, c, skip)
	}
	return r
}

func loadHealthChecks(ctx context.Context, r *Region, c GlobalClients, skip func(string, error)) {
	var hcs []HealthCheck
	used := map[string]bool{}
	p := route53.NewListHealthChecksPaginator(c.Route53, &route53.ListHealthChecksInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *route53.ListHealthChecksOutput) {
		for _, h := range o.HealthChecks {
			hc := HealthCheck{ID: aws.ToString(h.Id)}
			if s := h.LinkedService; s != nil {
				hc.Managed = aws.ToString(s.ServicePrincipal)
			}
			if cfg := h.HealthCheckConfig; cfg != nil {
				hc.Type, hc.Disabled = string(cfg.Type), aws.ToBool(cfg.Disabled)
				hc.Target = aws.ToString(cfg.FullyQualifiedDomainName)
				if hc.Target == "" {
					hc.Target = aws.ToString(cfg.IPAddress)
				}
				switch cfg.Type {
				case r53types.HealthCheckTypeHttps:
					hc.Options++
				case r53types.HealthCheckTypeHttpStrMatch:
					hc.Options++
				case r53types.HealthCheckTypeHttpsStrMatch:
					hc.Options += 2 // HTTPS and string matching
				case r53types.HealthCheckTypeRecoveryControl:
					hc.Used = true // an ARC routing control's own health check
				}
				if aws.ToInt32(cfg.RequestInterval) == 10 {
					hc.Options++ // fast interval
				}
				if aws.ToBool(cfg.MeasureLatency) {
					hc.Options++
				}
				if a := cfg.AlarmIdentifier; a != nil {
					hc.Alarm = string(a.Region) + "|" + aws.ToString(a.Name)
				}
				for _, child := range cfg.ChildHealthChecks {
					used[child] = true
				}
			}
			hcs = append(hcs, hc)
		}
	}); err != nil {
		skip("route53:ListHealthChecks", err)
		return
	}
	if len(hcs) == 0 {
		return
	}
	// Every record set that names a health check. Only the ID is kept: the
	// rest of each record is dropped here, never stored or printed.
	var zones []string
	if !r.ZonesListed {
		r.HealthCheckUsesUnknown = true // loadZones couldn't list them, and said so
	}
	for _, z := range r.Zones {
		zones = append(zones, "/hostedzone/"+z.ID)
	}
	for _, z := range zones {
		if r.HealthCheckUsesUnknown {
			break
		}
		in := &route53.ListResourceRecordSetsInput{HostedZoneId: aws.String(z)}
		for {
			o, err := c.Route53.ListResourceRecordSets(ctx, in)
			if err != nil {
				skip("route53:ListResourceRecordSets", err)
				r.HealthCheckUsesUnknown = true
				break
			}
			for _, rs := range o.ResourceRecordSets {
				if id := aws.ToString(rs.HealthCheckId); id != "" {
					used[id] = true
				}
			}
			if !o.IsTruncated {
				break
			}
			next := &route53.ListResourceRecordSetsInput{HostedZoneId: aws.String(z),
				StartRecordName: o.NextRecordName, StartRecordType: o.NextRecordType, StartRecordIdentifier: o.NextRecordIdentifier}
			if aws.ToString(next.StartRecordName) == aws.ToString(in.StartRecordName) && next.StartRecordType == in.StartRecordType &&
				aws.ToString(next.StartRecordIdentifier) == aws.ToString(in.StartRecordIdentifier) {
				skip("route53:ListResourceRecordSets", errors.New("the next page starts where this one did"))
				r.HealthCheckUsesUnknown = true // a page that doesn't move on: stop, unread
				break
			}
			in = next
		}
	}
	// Traffic policy instances route on health checks named in their policy
	// documents (not read here): any instance makes every finding uncertain.
	if tp, err := c.Route53.ListTrafficPolicyInstances(ctx, &route53.ListTrafficPolicyInstancesInput{}); err != nil {
		skip("route53:ListTrafficPolicyInstances", err)
		r.TrafficPolicies = true // unknown: as if there were some
	} else {
		r.TrafficPolicies = len(tp.TrafficPolicyInstances) > 0
	}
	watched := map[string]bool{}
	if c.Alarms == nil {
		r.HealthCheckAlarmsUnknown = true
	} else {
		ap := cloudwatch.NewDescribeAlarmsPaginator(c.Alarms, &cloudwatch.DescribeAlarmsInput{})
		if err := drain(ctx, ap.HasMorePages, ap.NextPage, func(o *cloudwatch.DescribeAlarmsOutput) {
			for _, a := range o.MetricAlarms {
				if aws.ToString(a.Namespace) == "AWS/Route53" {
					watchedHealthChecks(a.Dimensions, watched)
				}
				for _, m := range a.Metrics { // metric math
					if ms := m.MetricStat; ms != nil && ms.Metric != nil && aws.ToString(ms.Metric.Namespace) == "AWS/Route53" {
						watchedHealthChecks(ms.Metric.Dimensions, watched)
					}
				}
			}
		}); err != nil {
			skip("cloudwatch:DescribeAlarms", err)
			r.HealthCheckAlarmsUnknown = true
		}
	}
	for i := range hcs {
		hcs[i].Used = hcs[i].Used || used[hcs[i].ID]
		hcs[i].Watched = watched[hcs[i].ID]
	}
	r.HealthChecks = hcs
}

func watchedHealthChecks(dims []cwtypes.Dimension, into map[string]bool) {
	for _, d := range dims {
		if aws.ToString(d.Name) == "HealthCheckId" {
			into[aws.ToString(d.Value)] = true
		}
	}
}

// loadZones lists the account's hosted zones and, for a public zone with
// only its NS and SOA records, whether DNSSEC signs it (such a zone can't
// be deleted as it is, and no doc says what its record count includes).
func loadZones(ctx context.Context, r *Region, c GlobalClients, skip func(string, error)) {
	zp := route53.NewListHostedZonesPaginator(c.Route53, &route53.ListHostedZonesInput{})
	if err := drain(ctx, zp.HasMorePages, zp.NextPage, func(o *route53.ListHostedZonesOutput) {
		for _, z := range o.HostedZones {
			hz := HostedZone{ID: strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/"), Name: strings.TrimSuffix(aws.ToString(z.Name), "."),
				Records: aws.ToInt64(z.ResourceRecordSetCount)}
			if z.Config != nil {
				hz.Private = z.Config.PrivateZone
			}
			if s := z.LinkedService; s != nil {
				hz.Linked = aws.ToString(s.ServicePrincipal)
				if hz.Linked == "" {
					hz.Linked = "an AWS service"
				}
			}
			r.Zones = append(r.Zones, hz)
		}
	}); err != nil {
		skip("route53:ListHostedZones", err)
		r.Zones = nil
		return
	}
	r.ZonesListed = true
	failed := false
	for i, z := range r.Zones {
		if z.Private || z.Linked != "" || z.Records != 2 {
			continue
		}
		out, err := c.Route53.GetDNSSEC(ctx, &route53.GetDNSSECInput{HostedZoneId: aws.String(z.ID)})
		switch {
		case err != nil:
			r.Zones[i].DNSSEC = "unknown"
			if !failed {
				failed = true
				skip("route53:GetDNSSEC", err)
			}
		case len(out.KeySigningKeys) > 0 || (out.Status != nil && aws.ToString(out.Status.ServeSignature) != "NOT_SIGNING"):
			r.Zones[i].DNSSEC = "signed"
		default:
			r.Zones[i].DNSSEC = "none"
		}
	}
}
