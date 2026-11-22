package inventory

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
)

// Accelerator is a standard Global Accelerator. Custom routing accelerators
// aren't read: they have their own APIs and no health metrics to prove them
// idle.
type Accelerator struct {
	ARN, ID, Name, Status, IPType string
	Enabled                       bool
	Created                       *time.Time
	IPv4                          int // its static IPv4 addresses
	Listeners, Groups, Endpoints  int
	Unknown                       bool              // a listener or endpoint-group read failed: never judged
	Tags                          map[string]string // nil: not read
}

// TagRef is the accelerator's owner-tag reference.
func (a Accelerator) TagRef() string { return "globalaccelerator:accelerator/" + a.ID }

// AcceleratorAPI is what costhound reads of Global Accelerator, whose API
// lives in us-west-2.
type AcceleratorAPI interface {
	globalaccelerator.ListAcceleratorsAPIClient
	globalaccelerator.ListListenersAPIClient
	globalaccelerator.ListEndpointGroupsAPIClient
	ListTagsForResource(context.Context, *globalaccelerator.ListTagsForResourceInput, ...func(*globalaccelerator.Options)) (*globalaccelerator.ListTagsForResourceOutput, error)
}

// LoadAccelerators reads the account's standard accelerators, how many
// listeners, endpoint groups and endpoints each has, and their tags. A
// failed listener or endpoint-group read leaves that accelerator unknown;
// ok is false when the accelerators couldn't be listed at all.
func LoadAccelerators(ctx context.Context, c AcceleratorAPI, skip func(string, error)) (out []Accelerator, ok bool) {
	reported := map[string]bool{}
	note := func(what string, err error) {
		if !reported[what] {
			reported[what] = true
			skip(what, err)
		}
	}
	var list []gatypes.Accelerator
	p := globalaccelerator.NewListAcceleratorsPaginator(c, &globalaccelerator.ListAcceleratorsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *globalaccelerator.ListAcceleratorsOutput) { list = append(list, o.Accelerators...) }); err != nil {
		skip("globalaccelerator:ListAccelerators", err)
		return nil, false
	}
	for _, a := range list {
		arn := aws.ToString(a.AcceleratorArn)
		_, id, _ := strings.Cut(arn, ":accelerator/")
		acc := Accelerator{ARN: arn, ID: id, Name: aws.ToString(a.Name), Status: string(a.Status), IPType: string(a.IpAddressType),
			Enabled: aws.ToBool(a.Enabled), Created: a.CreatedTime}
		for _, s := range a.IpSets {
			if s.IpAddressFamily == gatypes.IpAddressFamilyIPv4 {
				acc.IPv4 += len(s.IpAddresses)
			}
		}
		var listeners []gatypes.Listener
		lp := globalaccelerator.NewListListenersPaginator(c, &globalaccelerator.ListListenersInput{AcceleratorArn: a.AcceleratorArn})
		if err := drain(ctx, lp.HasMorePages, lp.NextPage, func(o *globalaccelerator.ListListenersOutput) { listeners = append(listeners, o.Listeners...) }); err != nil {
			acc.Unknown = true
			note("globalaccelerator:ListListeners", err)
		}
		acc.Listeners = len(listeners)
		for _, l := range listeners {
			gp := globalaccelerator.NewListEndpointGroupsPaginator(c, &globalaccelerator.ListEndpointGroupsInput{ListenerArn: l.ListenerArn})
			if err := drain(ctx, gp.HasMorePages, gp.NextPage, func(o *globalaccelerator.ListEndpointGroupsOutput) {
				for _, g := range o.EndpointGroups {
					acc.Groups++
					acc.Endpoints += len(g.EndpointDescriptions)
				}
			}); err != nil {
				acc.Unknown = true
				note("globalaccelerator:ListEndpointGroups", err)
			}
		}
		if tags, err := c.ListTagsForResource(ctx, &globalaccelerator.ListTagsForResourceInput{ResourceArn: a.AcceleratorArn}); err != nil {
			note("globalaccelerator:ListTagsForResource", err)
		} else {
			acc.Tags = map[string]string{}
			for _, tg := range tags.Tags {
				acc.Tags[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
			}
		}
		out = append(out, acc)
	}
	return out, true
}
