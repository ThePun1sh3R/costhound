package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
)

const (
	WorkSpacesIdleDays    = 30
	workSpacesStatusBatch = 25
	workSpacesBundleBatch = 25
	workSpacesImageBatch  = 25
)

// WorkSpaces is a region's WorkSpaces Personal inventory for unused-workspace.
// A nil value on Region means the list failed or was never called.
type WorkSpaces struct {
	Items []WorkSpace
}

// WorkSpace is one Personal WorkSpace and the fields unused-workspace needs.
type WorkSpace struct {
	ID, DirectoryID, UserName, ComputerName, IP string
	State, BundleID                             string
	RunningMode, ComputeType, OS                string
	RootGiB, UserGiB                            int32
	Standby                                     bool
	StatusRead                                  bool
	ConnectionState                             string
	ConnectionChecked                           time.Time
	LastSignIn                                  time.Time // zero with StatusRead: never connected
	BYOL                                        bool
	LicenseUnread                               bool // bundle or image read failed
}

// WorkSpacesAPI is the WorkSpaces inventory unused-workspace reads.
type WorkSpacesAPI interface {
	workspaces.DescribeWorkspacesAPIClient
	DescribeWorkspacesConnectionStatus(context.Context, *workspaces.DescribeWorkspacesConnectionStatusInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesConnectionStatusOutput, error)
	workspaces.DescribeWorkspaceBundlesAPIClient
	DescribeWorkspaceImages(context.Context, *workspaces.DescribeWorkspaceImagesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceImagesOutput, error)
}

func loadWorkSpaces(ctx context.Context, r *Region, c WorkSpacesAPI, skip func(string, error)) {
	const op = "workspaces:DescribeWorkspaces"
	var items []WorkSpace
	standbyIDs := map[string]bool{}
	p := workspaces.NewDescribeWorkspacesPaginator(c, &workspaces.DescribeWorkspacesInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *workspaces.DescribeWorkspacesOutput) {
		for _, x := range o.Workspaces {
			id := aws.ToString(x.WorkspaceId)
			if id == "" {
				skip(op, errors.New("a listed WorkSpace is missing its ID"))
				continue
			}
			ws := WorkSpace{
				ID: id, DirectoryID: aws.ToString(x.DirectoryId),
				UserName: aws.ToString(x.UserName), ComputerName: aws.ToString(x.ComputerName),
				IP: aws.ToString(x.IpAddress), State: string(x.State), BundleID: aws.ToString(x.BundleId),
			}
			for _, rel := range x.RelatedWorkspaces {
				switch rel.Type {
				case wstypes.StandbyWorkspaceRelationshipTypeStandby:
					if rid := aws.ToString(rel.WorkspaceId); rid != "" {
						standbyIDs[rid] = true
					}
				case wstypes.StandbyWorkspaceRelationshipTypePrimary:
					standbyIDs[id] = true
				}
			}
			if props := x.WorkspaceProperties; props != nil {
				ws.RunningMode = string(props.RunningMode)
				ws.ComputeType = string(props.ComputeTypeName)
				ws.OS = string(props.OperatingSystemName)
				ws.RootGiB = aws.ToInt32(props.RootVolumeSizeGib)
				ws.UserGiB = aws.ToInt32(props.UserVolumeSizeGib)
			}
			items = append(items, ws)
		}
	}); err != nil {
		skip(op, err)
		return
	}
	for i := range items {
		items[i].Standby = standbyIDs[items[i].ID]
	}
	w := &WorkSpaces{Items: items}
	r.WorkSpaces = w
	if len(items) == 0 {
		return
	}
	loadWorkSpacesConnectionStatus(ctx, w, c, skip)
	loadWorkSpacesBYOL(ctx, w, c, skip)
}

func loadWorkSpacesConnectionStatus(ctx context.Context, w *WorkSpaces, c WorkSpacesAPI, skip func(string, error)) {
	const op = "workspaces:DescribeWorkspacesConnectionStatus"
	byID := map[string]*WorkSpace{}
	var ids []string
	for i := range w.Items {
		ws := &w.Items[i]
		byID[ws.ID] = ws
		ids = append(ids, ws.ID)
	}
	for from := 0; from < len(ids); from += workSpacesStatusBatch {
		batch := ids[from:min(from+workSpacesStatusBatch, len(ids))]
		seen := map[string]bool{}
		var token *string
		for {
			out, err := c.DescribeWorkspacesConnectionStatus(ctx, &workspaces.DescribeWorkspacesConnectionStatusInput{
				WorkspaceIds: batch, NextToken: token,
			})
			if err != nil {
				skip(op, err)
				break
			}
			for _, s := range out.WorkspacesConnectionStatus {
				id := aws.ToString(s.WorkspaceId)
				ws := byID[id]
				if ws == nil {
					continue
				}
				seen[id] = true
				ws.StatusRead = true
				ws.ConnectionState = string(s.ConnectionState)
				ws.ConnectionChecked = aws.ToTime(s.ConnectionStateCheckTimestamp)
				ws.LastSignIn = aws.ToTime(s.LastKnownUserConnectionTimestamp)
			}
			if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
				break
			}
			token = out.NextToken
		}
		for _, id := range batch {
			if !seen[id] {
				// Missing from a successful answer: unread, never "never connected".
				byID[id].StatusRead = false
			}
		}
	}
}

func loadWorkSpacesBYOL(ctx context.Context, w *WorkSpaces, c WorkSpacesAPI, skip func(string, error)) {
	bundleIDs := map[string]bool{}
	for _, ws := range w.Items {
		if ws.BundleID != "" {
			bundleIDs[ws.BundleID] = true
		}
	}
	if len(bundleIDs) == 0 {
		return
	}
	bundleImage, bundleOK := map[string]string{}, true
	var ids []string
	for id := range bundleIDs {
		ids = append(ids, id)
	}
	for from := 0; from < len(ids); from += workSpacesBundleBatch {
		batch := ids[from:min(from+workSpacesBundleBatch, len(ids))]
		var token *string
		found := map[string]bool{}
		for {
			out, err := c.DescribeWorkspaceBundles(ctx, &workspaces.DescribeWorkspaceBundlesInput{
				BundleIds: batch, NextToken: token,
			})
			if err != nil {
				skip("workspaces:DescribeWorkspaceBundles", err)
				bundleOK = false
				break
			}
			for _, b := range out.Bundles {
				id, image := aws.ToString(b.BundleId), aws.ToString(b.ImageId)
				if id == "" {
					continue
				}
				found[id] = true
				bundleImage[id] = image
			}
			if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
				break
			}
			token = out.NextToken
		}
		if !bundleOK {
			break
		}
		for _, id := range batch {
			if !found[id] {
				bundleOK = false
			}
		}
	}
	if !bundleOK {
		for i := range w.Items {
			w.Items[i].LicenseUnread = true
		}
		return
	}

	imageIDs := map[string]bool{}
	for _, image := range bundleImage {
		if image != "" {
			imageIDs[image] = true
		}
	}
	byol := map[string]bool{}
	imageOK := true
	var images []string
	for id := range imageIDs {
		images = append(images, id)
	}
	for from := 0; from < len(images); from += workSpacesImageBatch {
		batch := images[from:min(from+workSpacesImageBatch, len(images))]
		var token *string
		found := map[string]bool{}
		for {
			out, err := c.DescribeWorkspaceImages(ctx, &workspaces.DescribeWorkspaceImagesInput{
				ImageIds: batch, NextToken: token,
			})
			if err != nil {
				skip("workspaces:DescribeWorkspaceImages", err)
				imageOK = false
				break
			}
			for _, img := range out.Images {
				id := aws.ToString(img.ImageId)
				if id == "" {
					continue
				}
				found[id] = true
				byol[id] = img.RequiredTenancy == wstypes.WorkspaceImageRequiredTenancyDedicated
			}
			if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
				break
			}
			token = out.NextToken
		}
		if !imageOK {
			break
		}
		for _, id := range batch {
			if !found[id] {
				imageOK = false
			}
		}
	}
	if !imageOK {
		for i := range w.Items {
			w.Items[i].LicenseUnread = true
		}
		return
	}
	for i := range w.Items {
		ws := &w.Items[i]
		image := bundleImage[ws.BundleID]
		if ws.BundleID == "" || image == "" {
			ws.LicenseUnread = true
			continue
		}
		ws.BYOL = byol[image]
	}
}
