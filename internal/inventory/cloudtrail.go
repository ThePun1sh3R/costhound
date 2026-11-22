package inventory

import (
	"context"
	"errors"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
)

// CloudTrailAPI reads trails and their settings: never events or logs.
type CloudTrailAPI interface {
	DescribeTrails(context.Context, *cloudtrail.DescribeTrailsInput, ...func(*cloudtrail.Options)) (*cloudtrail.DescribeTrailsOutput, error)
	GetTrailStatus(context.Context, *cloudtrail.GetTrailStatusInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetTrailStatusOutput, error)
	GetEventSelectors(context.Context, *cloudtrail.GetEventSelectorsInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetEventSelectorsOutput, error)
}

// Trail is a trail that logs in this region: one homed here, a multi-Region
// trail homed elsewhere, or an organization trail.
type Trail struct {
	Name, ARN, HomeRegion     string
	MultiRegion, Organization bool
	Logging                   bool
	// DeliveryFailing: its last delivery to S3 failed, so it may deliver,
	// and bill, nothing: it isn't counted as a copy.
	DeliveryFailing bool
	// Management is the management events it copies: "all", "read",
	// "write", or "" for none. Event sources it leaves out (KMS, RDS Data
	// API) only make the copy smaller, so they don't change it.
	Management string
	// Unknown says why its status or selectors couldn't be read ("" when
	// they were): any unknown trail makes the region's copies unknown.
	Unknown string
}

func loadTrails(ctx context.Context, r *Region, c CloudTrailAPI, skip func(string, error)) {
	out, err := c.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{IncludeShadowTrails: aws.Bool(true)})
	if err != nil {
		skip("cloudtrail:DescribeTrails", err)
		return
	}
	// Every trail costhound can't read is a skip line (once per call), so a
	// region whose copies are unknown never looks clean: a member account
	// that may not read the organization trail's settings included.
	reported := map[string]bool{}
	note := func(what string, err error) string {
		if !reported[what] {
			reported[what] = true
			skip(what, err)
		}
		return what + ": " + ShortError(err)
	}
	trails := []Trail{}
	for _, t := range out.TrailList {
		tr := Trail{Name: aws.ToString(t.Name), ARN: aws.ToString(t.TrailARN), HomeRegion: aws.ToString(t.HomeRegion),
			MultiRegion: aws.ToBool(t.IsMultiRegionTrail), Organization: aws.ToBool(t.IsOrganizationTrail)}
		// The full ARN: a member account must name an organization trail by it.
		st, err := c.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: t.TrailARN})
		if err != nil {
			tr.Unknown = note("cloudtrail:GetTrailStatus", err)
			trails = append(trails, tr)
			continue
		}
		tr.Logging, tr.DeliveryFailing = aws.ToBool(st.IsLogging), aws.ToString(st.LatestDeliveryError) != ""
		sel, err := c.GetEventSelectors(ctx, &cloudtrail.GetEventSelectorsInput{TrailName: t.TrailARN})
		if err != nil {
			tr.Unknown = note("cloudtrail:GetEventSelectors", err)
		} else if m, ok := managementEvents(sel.EventSelectors, sel.AdvancedEventSelectors); ok {
			tr.Management = m
		} else {
			tr.Unknown = note("cloudtrail:GetEventSelectors", errors.New("event selectors costhound doesn't read: this region's duplicate trails are unknown"))
		}
		trails = append(trails, tr)
	}
	sort.Slice(trails, func(i, j int) bool { return trails[i].ARN < trails[j].ARN })
	r.Trails = trails
}

// managementEvents reads which management events a trail copies: "all",
// "read", "write" or "" (none). Management selectors use only
// eventCategory, readOnly and eventSource NotEquals (KMS, RDS Data API);
// anything else, or no selectors at all, is unread (false).
func managementEvents(basic []cttypes.EventSelector, advanced []cttypes.AdvancedEventSelector) (string, bool) {
	read, write := false, false
	switch {
	case len(advanced) > 0:
		for _, s := range advanced {
			management, mode, ok := false, "all", true
			for _, f := range s.FieldSelectors {
				switch field := aws.ToString(f.Field); {
				case field == "eventCategory" && len(f.Equals) == 1 && f.Equals[0] == "Management" && onlyEquals(f):
					management = true
				case field == "eventCategory":
					// Data, NetworkActivity, ...: not management events
				case field == "readOnly" && len(f.Equals) == 1 && onlyEquals(f):
					mode = map[string]string{"true": "read", "false": "write"}[f.Equals[0]]
					ok = ok && mode != ""
				case field == "eventSource" && len(f.Equals) == 0 && len(f.StartsWith)+len(f.EndsWith)+len(f.NotStartsWith)+len(f.NotEndsWith) == 0:
					// NotEquals kms/rdsdata: a smaller copy, still a copy
				default:
					ok = false
				}
			}
			if !management {
				continue
			}
			if !ok {
				return "", false
			}
			read = read || mode != "write"
			write = write || mode != "read"
		}
	case len(basic) > 0:
		for _, s := range basic {
			if !aws.ToBool(s.IncludeManagementEvents) {
				continue
			}
			switch s.ReadWriteType {
			case cttypes.ReadWriteTypeAll:
				read, write = true, true
			case cttypes.ReadWriteTypeReadOnly:
				read = true
			case cttypes.ReadWriteTypeWriteOnly:
				write = true
			default:
				return "", false
			}
		}
	default:
		return "", false // AWS always lists a trail's selectors: none is unread
	}
	switch {
	case read && write:
		return "all", true
	case read:
		return "read", true
	case write:
		return "write", true
	}
	return "", true
}

func onlyEquals(f cttypes.AdvancedFieldSelector) bool {
	return len(f.NotEquals)+len(f.StartsWith)+len(f.EndsWith)+len(f.NotStartsWith)+len(f.NotEndsWith) == 0
}
