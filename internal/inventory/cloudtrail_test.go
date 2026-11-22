package inventory

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"
)

const orgTrailARN = "arn:aws:cloudtrail:us-east-1:999999999999:trail/org-trail"

type fakeTrails struct{ statusAsked []string }

func (f *fakeTrails) DescribeTrails(_ context.Context, in *cloudtrail.DescribeTrailsInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.DescribeTrailsOutput, error) {
	if !aws.ToBool(in.IncludeShadowTrails) {
		return nil, &smithy.GenericAPIError{Code: "Unexpected", Message: "shadow trails must be included"}
	}
	tr := func(name, home string, mr, org bool) cttypes.Trail {
		arn := "arn:aws:cloudtrail:" + home + ":123456789012:trail/" + name
		if org {
			arn = orgTrailARN
		}
		return cttypes.Trail{Name: aws.String(name), TrailARN: aws.String(arn), HomeRegion: aws.String(home), IsMultiRegionTrail: aws.Bool(mr), IsOrganizationTrail: aws.Bool(org)}
	}
	return &cloudtrail.DescribeTrailsOutput{TrailList: []cttypes.Trail{tr("org-trail", "us-east-1", true, true), tr("audit", "eu-west-1", false, false),
		tr("adv", "eu-west-1", false, false), tr("odd", "eu-west-1", false, false), tr("locked", "eu-west-1", false, false)}}, nil
}

func (f *fakeTrails) GetTrailStatus(_ context.Context, in *cloudtrail.GetTrailStatusInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.GetTrailStatusOutput, error) {
	f.statusAsked = append(f.statusAsked, aws.ToString(in.Name))
	if strings.HasSuffix(aws.ToString(in.Name), "/audit") {
		return &cloudtrail.GetTrailStatusOutput{IsLogging: aws.Bool(true), LatestDeliveryError: aws.String("AccessDenied")}, nil
	}
	return &cloudtrail.GetTrailStatusOutput{IsLogging: aws.Bool(true)}, nil
}

func fieldEq(field string, v ...string) cttypes.AdvancedFieldSelector {
	return cttypes.AdvancedFieldSelector{Field: aws.String(field), Equals: v}
}

func (f *fakeTrails) GetEventSelectors(_ context.Context, in *cloudtrail.GetEventSelectorsInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.GetEventSelectorsOutput, error) {
	switch name := aws.ToString(in.TrailName); {
	case name == orgTrailARN || strings.HasSuffix(name, "/audit"):
		return &cloudtrail.GetEventSelectorsOutput{EventSelectors: []cttypes.EventSelector{{ReadWriteType: cttypes.ReadWriteTypeAll, IncludeManagementEvents: aws.Bool(true)}}}, nil
	case strings.HasSuffix(name, "/adv"): // write management events without KMS, plus S3 data events
		return &cloudtrail.GetEventSelectorsOutput{AdvancedEventSelectors: []cttypes.AdvancedEventSelector{
			{FieldSelectors: []cttypes.AdvancedFieldSelector{fieldEq("eventCategory", "Management"), fieldEq("readOnly", "false"),
				{Field: aws.String("eventSource"), NotEquals: []string{"kms.amazonaws.com"}}}},
			{FieldSelectors: []cttypes.AdvancedFieldSelector{fieldEq("eventCategory", "Data"), fieldEq("resources.type", "AWS::S3::Object")}}}}, nil
	case strings.HasSuffix(name, "/odd"): // a management selector costhound doesn't read
		return &cloudtrail.GetEventSelectorsOutput{AdvancedEventSelectors: []cttypes.AdvancedEventSelector{
			{FieldSelectors: []cttypes.AdvancedFieldSelector{fieldEq("eventCategory", "Management"), fieldEq("eventName", "ConsoleLogin")}}}}, nil
	}
	return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
}

func TestLoadTrails(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	var skips []string
	f := &fakeTrails{}
	loadTrails(context.Background(), r, f, func(what string, _ error) { skips = append(skips, what) })
	got := map[string]Trail{}
	for _, tr := range r.Trails {
		got[tr.Name] = tr
	}
	if o := got["org-trail"]; !o.Organization || !o.MultiRegion || o.Management != "all" || !o.Logging || o.Unknown != "" {
		t.Errorf("org trail: %+v", o)
	}
	if !strings.Contains(strings.Join(f.statusAsked, " "), orgTrailARN) {
		t.Errorf("a member names an organization trail by its full ARN: %v", f.statusAsked)
	}
	if got["audit"].Management != "all" || !got["audit"].DeliveryFailing || got["adv"].Management != "write" || got["adv"].Unknown != "" || got["adv"].DeliveryFailing {
		t.Errorf("basic all; advanced write without KMS (data events aside): %+v %+v", got["audit"], got["adv"])
	}
	if got["odd"].Unknown == "" || got["locked"].Unknown == "" {
		t.Errorf("an unread selector and a denied read are unknown: %+v %+v", got["odd"], got["locked"])
	}
	if len(skips) != 1 || skips[0] != "cloudtrail:GetEventSelectors" {
		t.Errorf("an unread trail is a skip line, once per call: %v", skips)
	}
}

func TestManagementEvents(t *testing.T) {
	sel := func(rw cttypes.ReadWriteType, mgmt bool) cttypes.EventSelector {
		return cttypes.EventSelector{ReadWriteType: rw, IncludeManagementEvents: aws.Bool(mgmt)}
	}
	for _, c := range []struct {
		basic    []cttypes.EventSelector
		advanced []cttypes.AdvancedEventSelector
		want     string
		ok       bool
	}{
		{basic: []cttypes.EventSelector{sel(cttypes.ReadWriteTypeReadOnly, true), sel(cttypes.ReadWriteTypeWriteOnly, true)}, want: "all", ok: true},
		{basic: []cttypes.EventSelector{sel(cttypes.ReadWriteTypeAll, false)}, want: "", ok: true}, // data events only
		{advanced: []cttypes.AdvancedEventSelector{{FieldSelectors: []cttypes.AdvancedFieldSelector{fieldEq("eventCategory", "Management"), fieldEq("readOnly", "true")}}}, want: "read", ok: true},
		{want: "", ok: false}, // no selectors at all: unread
	} {
		if got, ok := managementEvents(c.basic, c.advanced); got != c.want || ok != c.ok {
			t.Errorf("%+v %+v: %q %v, want %q %v", c.basic, c.advanced, got, ok, c.want, c.ok)
		}
	}
}
