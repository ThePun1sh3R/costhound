package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
)

type fakeWorkSpaces struct {
	list    func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error)
	status  func(*workspaces.DescribeWorkspacesConnectionStatusInput) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error)
	bundles func(*workspaces.DescribeWorkspaceBundlesInput) (*workspaces.DescribeWorkspaceBundlesOutput, error)
	images  func(*workspaces.DescribeWorkspaceImagesInput) (*workspaces.DescribeWorkspaceImagesOutput, error)
}

func (f fakeWorkSpaces) DescribeWorkspaces(_ context.Context, in *workspaces.DescribeWorkspacesInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesOutput, error) {
	return f.list(in)
}

func (f fakeWorkSpaces) DescribeWorkspacesConnectionStatus(_ context.Context, in *workspaces.DescribeWorkspacesConnectionStatusInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error) {
	return f.status(in)
}

func (f fakeWorkSpaces) DescribeWorkspaceBundles(_ context.Context, in *workspaces.DescribeWorkspaceBundlesInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
	return f.bundles(in)
}

func (f fakeWorkSpaces) DescribeWorkspaceImages(_ context.Context, in *workspaces.DescribeWorkspaceImagesInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceImagesOutput, error) {
	return f.images(in)
}

func TestLoadWorkSpacesBatchesConnectionStatus(t *testing.T) {
	var statusCalls []int
	var items []wstypes.Workspace
	for i := 0; i < 30; i++ {
		items = append(items, wstypes.Workspace{
			WorkspaceId: aws.String(fmt.Sprintf("ws-%02d", i)),
			DirectoryId: aws.String("d-1"),
			UserName:    aws.String("user"),
			State:       wstypes.WorkspaceStateAvailable,
			BundleId:    aws.String("wsb-1"),
			WorkspaceProperties: &wstypes.WorkspaceProperties{
				RunningMode: wstypes.RunningModeAlwaysOn, ComputeTypeName: wstypes.ComputeStandard,
				OperatingSystemName: wstypes.OperatingSystemNameWindows10,
				RootVolumeSizeGib:   aws.Int32(80), UserVolumeSizeGib: aws.Int32(50),
			},
		})
	}
	c := fakeWorkSpaces{
		list: func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error) {
			return &workspaces.DescribeWorkspacesOutput{Workspaces: items}, nil
		},
		status: func(in *workspaces.DescribeWorkspacesConnectionStatusInput) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error) {
			statusCalls = append(statusCalls, len(in.WorkspaceIds))
			var out []wstypes.WorkspaceConnectionStatus
			for _, id := range in.WorkspaceIds {
				out = append(out, wstypes.WorkspaceConnectionStatus{
					WorkspaceId: aws.String(id), ConnectionState: wstypes.ConnectionStateDisconnected,
					LastKnownUserConnectionTimestamp: aws.Time(time.Unix(1, 0)),
				})
			}
			return &workspaces.DescribeWorkspacesConnectionStatusOutput{WorkspacesConnectionStatus: out}, nil
		},
		bundles: func(in *workspaces.DescribeWorkspaceBundlesInput) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
			return &workspaces.DescribeWorkspaceBundlesOutput{Bundles: []wstypes.WorkspaceBundle{
				{BundleId: aws.String("wsb-1"), ImageId: aws.String("wsi-1")},
			}}, nil
		},
		images: func(*workspaces.DescribeWorkspaceImagesInput) (*workspaces.DescribeWorkspaceImagesOutput, error) {
			return &workspaces.DescribeWorkspaceImagesOutput{Images: []wstypes.WorkspaceImage{
				{ImageId: aws.String("wsi-1"), RequiredTenancy: wstypes.WorkspaceImageRequiredTenancyDefault},
			}}, nil
		},
	}
	r := &Region{}
	loadWorkSpaces(t.Context(), r, c, func(op string, _ error) { t.Errorf("unexpected skip %s", op) })
	if !slices.Equal(statusCalls, []int{25, 5}) || len(r.WorkSpaces.Items) != 30 {
		t.Fatalf("want 25+5 connection-status calls over 30 WorkSpaces, got %v items=%d", statusCalls, len(r.WorkSpaces.Items))
	}
	for _, ws := range r.WorkSpaces.Items {
		if !ws.StatusRead || ws.LastSignIn.IsZero() {
			t.Fatalf("status unread: %+v", ws)
		}
	}
}

func TestLoadWorkSpacesFailedStatusBatchLeavesUnread(t *testing.T) {
	c := fakeWorkSpaces{
		list: func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error) {
			return &workspaces.DescribeWorkspacesOutput{Workspaces: []wstypes.Workspace{
				{WorkspaceId: aws.String("ws-a"), BundleId: aws.String("wsb-1")},
				{WorkspaceId: aws.String("ws-b"), BundleId: aws.String("wsb-1")},
			}}, nil
		},
		status: func(*workspaces.DescribeWorkspacesConnectionStatusInput) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error) {
			return nil, errors.New("denied")
		},
		bundles: func(*workspaces.DescribeWorkspaceBundlesInput) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
			return &workspaces.DescribeWorkspaceBundlesOutput{Bundles: []wstypes.WorkspaceBundle{
				{BundleId: aws.String("wsb-1"), ImageId: aws.String("wsi-1")},
			}}, nil
		},
		images: func(*workspaces.DescribeWorkspaceImagesInput) (*workspaces.DescribeWorkspaceImagesOutput, error) {
			return &workspaces.DescribeWorkspaceImagesOutput{Images: []wstypes.WorkspaceImage{
				{ImageId: aws.String("wsi-1")},
			}}, nil
		},
	}
	r := &Region{}
	var skips []string
	loadWorkSpaces(t.Context(), r, c, func(op string, _ error) { skips = append(skips, op) })
	if r.WorkSpaces.Items[0].StatusRead || r.WorkSpaces.Items[1].StatusRead {
		t.Fatal("failed batch must leave status unread")
	}
	if !slices.Equal(skips, []string{"workspaces:DescribeWorkspacesConnectionStatus"}) {
		t.Errorf("skips = %v", skips)
	}
}

func TestLoadWorkSpacesMissingStatusIDLeavesUnread(t *testing.T) {
	c := fakeWorkSpaces{
		list: func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error) {
			return &workspaces.DescribeWorkspacesOutput{Workspaces: []wstypes.Workspace{
				{WorkspaceId: aws.String("ws-a"), BundleId: aws.String("wsb-1")},
				{WorkspaceId: aws.String("ws-b"), BundleId: aws.String("wsb-1")},
			}}, nil
		},
		status: func(*workspaces.DescribeWorkspacesConnectionStatusInput) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error) {
			return &workspaces.DescribeWorkspacesConnectionStatusOutput{WorkspacesConnectionStatus: []wstypes.WorkspaceConnectionStatus{
				{WorkspaceId: aws.String("ws-a"), ConnectionState: wstypes.ConnectionStateDisconnected},
			}}, nil
		},
		bundles: func(*workspaces.DescribeWorkspaceBundlesInput) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
			return &workspaces.DescribeWorkspaceBundlesOutput{Bundles: []wstypes.WorkspaceBundle{
				{BundleId: aws.String("wsb-1"), ImageId: aws.String("wsi-1")},
			}}, nil
		},
		images: func(*workspaces.DescribeWorkspaceImagesInput) (*workspaces.DescribeWorkspaceImagesOutput, error) {
			return &workspaces.DescribeWorkspaceImagesOutput{Images: []wstypes.WorkspaceImage{
				{ImageId: aws.String("wsi-1")},
			}}, nil
		},
	}
	r := &Region{}
	loadWorkSpaces(t.Context(), r, c, func(string, error) {})
	if !r.WorkSpaces.Items[0].StatusRead || r.WorkSpaces.Items[1].StatusRead {
		t.Fatalf("missing ID must leave only that WorkSpace unread: %+v", r.WorkSpaces.Items)
	}
}

func TestLoadWorkSpacesBYOLFromDedicatedTenancy(t *testing.T) {
	c := fakeWorkSpaces{
		list: func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error) {
			return &workspaces.DescribeWorkspacesOutput{Workspaces: []wstypes.Workspace{
				{WorkspaceId: aws.String("ws-byol"), BundleId: aws.String("wsb-byol"),
					UserName: aws.String("Jane Doe"), ComputerName: aws.String("WS-PC"), IpAddress: aws.String("10.0.0.5"),
					RelatedWorkspaces: []wstypes.RelatedWorkspaceProperties{
						{WorkspaceId: aws.String("ws-standby"), Type: wstypes.StandbyWorkspaceRelationshipTypeStandby},
					}},
				{WorkspaceId: aws.String("ws-standby"), BundleId: aws.String("wsb-byol"),
					RelatedWorkspaces: []wstypes.RelatedWorkspaceProperties{
						{WorkspaceId: aws.String("ws-byol"), Type: wstypes.StandbyWorkspaceRelationshipTypePrimary},
					}},
			}}, nil
		},
		status: func(in *workspaces.DescribeWorkspacesConnectionStatusInput) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error) {
			var out []wstypes.WorkspaceConnectionStatus
			for _, id := range in.WorkspaceIds {
				out = append(out, wstypes.WorkspaceConnectionStatus{WorkspaceId: aws.String(id), ConnectionState: wstypes.ConnectionStateUnknown})
			}
			return &workspaces.DescribeWorkspacesConnectionStatusOutput{WorkspacesConnectionStatus: out}, nil
		},
		bundles: func(*workspaces.DescribeWorkspaceBundlesInput) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
			return &workspaces.DescribeWorkspaceBundlesOutput{Bundles: []wstypes.WorkspaceBundle{
				{BundleId: aws.String("wsb-byol"), ImageId: aws.String("wsi-byol")},
			}}, nil
		},
		images: func(*workspaces.DescribeWorkspaceImagesInput) (*workspaces.DescribeWorkspaceImagesOutput, error) {
			return &workspaces.DescribeWorkspaceImagesOutput{Images: []wstypes.WorkspaceImage{
				{ImageId: aws.String("wsi-byol"), RequiredTenancy: wstypes.WorkspaceImageRequiredTenancyDedicated},
			}}, nil
		},
	}
	r := &Region{}
	loadWorkSpaces(t.Context(), r, c, func(string, error) {})
	byID := map[string]WorkSpace{}
	for _, ws := range r.WorkSpaces.Items {
		byID[ws.ID] = ws
	}
	if !byID["ws-byol"].BYOL || !byID["ws-standby"].BYOL || !byID["ws-standby"].Standby || byID["ws-byol"].Standby {
		t.Fatalf("BYOL/standby flags wrong: %+v", byID)
	}
	if byID["ws-byol"].UserName != "Jane Doe" || byID["ws-byol"].ComputerName != "WS-PC" || byID["ws-byol"].IP != "10.0.0.5" {
		t.Fatalf("names not kept: %+v", byID["ws-byol"])
	}
	encoded, err := json.Marshal(r.WorkSpaces)
	if err != nil || strings.Contains(string(encoded), "registration") || strings.Contains(string(encoded), "DirectoryService") {
		t.Fatal("directory secrets must never be loaded")
	}
}

func TestLoadWorkSpacesListFailureLeavesNil(t *testing.T) {
	c := fakeWorkSpaces{
		list: func(*workspaces.DescribeWorkspacesInput) (*workspaces.DescribeWorkspacesOutput, error) {
			return nil, errors.New("denied")
		},
	}
	r := &Region{}
	loadWorkSpaces(t.Context(), r, c, func(string, error) {})
	if r.WorkSpaces != nil {
		t.Fatal("failed list must leave WorkSpaces nil")
	}
}
