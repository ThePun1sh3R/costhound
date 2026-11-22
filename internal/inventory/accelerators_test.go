package inventory

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"
	gatypes "github.com/aws/aws-sdk-go-v2/service/globalaccelerator/types"
)

// fakeGA: two accelerators over two pages; the second one's endpoint groups
// can't be read, and its tags fail too.
type fakeGA struct{}

const gaARN = "arn:aws:globalaccelerator::123456789012:accelerator/"

func (fakeGA) ListAccelerators(_ context.Context, in *globalaccelerator.ListAcceleratorsInput, _ ...func(*globalaccelerator.Options)) (*globalaccelerator.ListAcceleratorsOutput, error) {
	if in.NextToken == nil {
		return &globalaccelerator.ListAcceleratorsOutput{NextToken: aws.String("p2"), Accelerators: []gatypes.Accelerator{{
			AcceleratorArn: aws.String(gaARN + "a1"), Name: aws.String("edge"), Enabled: aws.Bool(true), Status: gatypes.AcceleratorStatusDeployed,
			IpAddressType: gatypes.IpAddressTypeDualStack,
			IpSets: []gatypes.IpSet{{IpAddressFamily: gatypes.IpAddressFamilyIPv4, IpAddresses: []string{"198.51.100.1", "198.51.100.2"}},
				{IpAddressFamily: gatypes.IpAddressFamilyIPv6, IpAddresses: []string{"2001:db8::1", "2001:db8::2"}}}}}}, nil
	}
	return &globalaccelerator.ListAcceleratorsOutput{Accelerators: []gatypes.Accelerator{{AcceleratorArn: aws.String(gaARN + "a2"), Name: aws.String("broken")}}}, nil
}

func (fakeGA) ListListeners(_ context.Context, in *globalaccelerator.ListListenersInput, _ ...func(*globalaccelerator.Options)) (*globalaccelerator.ListListenersOutput, error) {
	arn := aws.ToString(in.AcceleratorArn)
	return &globalaccelerator.ListListenersOutput{Listeners: []gatypes.Listener{{ListenerArn: aws.String(arn + "/listener/l1")}, {ListenerArn: aws.String(arn + "/listener/l2")}}}, nil
}

func (fakeGA) ListEndpointGroups(_ context.Context, in *globalaccelerator.ListEndpointGroupsInput, _ ...func(*globalaccelerator.Options)) (*globalaccelerator.ListEndpointGroupsOutput, error) {
	switch aws.ToString(in.ListenerArn) {
	case gaARN + "a1/listener/l1":
		return &globalaccelerator.ListEndpointGroupsOutput{EndpointGroups: []gatypes.EndpointGroup{{EndpointDescriptions: []gatypes.EndpointDescription{{EndpointId: aws.String("lb-1")}, {EndpointId: aws.String("lb-2")}}}}}, nil
	case gaARN + "a1/listener/l2":
		return &globalaccelerator.ListEndpointGroupsOutput{EndpointGroups: []gatypes.EndpointGroup{{}}}, nil
	}
	return nil, fmt.Errorf("AccessDeniedException")
}

func (fakeGA) ListTagsForResource(_ context.Context, in *globalaccelerator.ListTagsForResourceInput, _ ...func(*globalaccelerator.Options)) (*globalaccelerator.ListTagsForResourceOutput, error) {
	if aws.ToString(in.ResourceArn) == gaARN+"a1" {
		return &globalaccelerator.ListTagsForResourceOutput{Tags: []gatypes.Tag{{Key: aws.String("team"), Value: aws.String("edge")}}}, nil
	}
	return nil, fmt.Errorf("ThrottlingException")
}

func TestLoadAccelerators(t *testing.T) {
	var skips []string
	got, ok := LoadAccelerators(context.Background(), fakeGA{}, func(what string, _ error) { skips = append(skips, what) })
	if !ok || len(got) != 2 {
		t.Fatalf("both pages: %+v", got)
	}
	a := got[0]
	if a.ID != "a1" || a.IPv4 != 2 || a.Listeners != 2 || a.Groups != 2 || a.Endpoints != 2 || a.Unknown || a.Tags["team"] != "edge" || !a.Enabled {
		t.Errorf("a1: %+v", a)
	}
	if b := got[1]; !b.Unknown || b.Tags != nil || b.TagRef() != "globalaccelerator:accelerator/a2" {
		t.Errorf("a2's endpoint groups and tags failed: unknown, no tags: %+v", b)
	}
	if fmt.Sprint(skips) != "[globalaccelerator:ListEndpointGroups globalaccelerator:ListTagsForResource]" {
		t.Errorf("each failing call once: %v", skips)
	}
}
