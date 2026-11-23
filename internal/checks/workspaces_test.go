package checks

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func wsBase() inventory.WorkSpace {
	return inventory.WorkSpace{
		ID: "ws-1", DirectoryID: "d-1", UserName: "jane", State: "AVAILABLE",
		RunningMode: "ALWAYS_ON", ComputeType: "STANDARD", OS: "WINDOWS_10",
		RootGiB: 80, UserGiB: 50, StatusRead: true, ConnectionState: "DISCONNECTED",
		ConnectionChecked: now, LastSignIn: now.AddDate(0, 0, -40),
	}
}

func TestUnusedWorkSpacesRules(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mutate       func(*inventory.WorkSpace)
		extra        []inventory.WorkSpace
		wantFinding  bool
		wantUnjudged bool
		wantNothing  bool
		conf         string
		usd          float64
		detailHas    string
		fixHas       string
	}{
		{name: "connected", mutate: func(ws *inventory.WorkSpace) { ws.ConnectionState = "CONNECTED" }, wantUnjudged: true},
		{name: "unhealthy", mutate: func(ws *inventory.WorkSpace) { ws.State = "UNHEALTHY" }, wantUnjudged: true},
		{name: "manual", mutate: func(ws *inventory.WorkSpace) { ws.RunningMode = "MANUAL" }, wantUnjudged: true},
		{name: "standby", mutate: func(ws *inventory.WorkSpace) { ws.Standby = true }, wantUnjudged: true},
		{name: "terminated", mutate: func(ws *inventory.WorkSpace) { ws.State = "TERMINATED" }, wantNothing: true},
		{name: "terminating", mutate: func(ws *inventory.WorkSpace) { ws.State = "TERMINATING" }, wantNothing: true},
		{name: "30 days", mutate: func(ws *inventory.WorkSpace) { ws.LastSignIn = now.AddDate(0, 0, -30) },
			wantFinding: true, conf: "medium", usd: 35, detailHas: "us-east-1 price"},
		{name: "89 days", mutate: func(ws *inventory.WorkSpace) { ws.LastSignIn = now.AddDate(0, 0, -89) },
			wantFinding: true, conf: "medium", usd: 35},
		{name: "90 days", mutate: func(ws *inventory.WorkSpace) { ws.LastSignIn = now.AddDate(0, 0, -90) },
			wantFinding: true, conf: "high", usd: 35},
		{name: "recent", mutate: func(ws *inventory.WorkSpace) { ws.LastSignIn = now.AddDate(0, 0, -10) }, wantUnjudged: true},
		{name: "never connected free", mutate: func(ws *inventory.WorkSpace) { ws.LastSignIn = time.Time{} }, wantUnjudged: true},
		{name: "missing status", mutate: func(ws *inventory.WorkSpace) { ws.StatusRead = false }, wantUnjudged: true},
		{name: "byol under 50", mutate: func(ws *inventory.WorkSpace) { ws.BYOL = true },
			wantFinding: true, conf: "low", detailHas: "at least 50 WorkSpaces a region (most bundles; linked accounts count together), and this account would keep 0 here"},
		{name: "autostop fee", mutate: func(ws *inventory.WorkSpace) { ws.RunningMode = "AUTO_STOP" },
			wantFinding: true, conf: "medium", usd: 9.75, detailHas: "; billed $9.75 a month, its fixed AutoStop fee; a terminate saves it from next month"},
		{name: "license unread", mutate: func(ws *inventory.WorkSpace) { ws.LicenseUnread = true },
			wantFinding: true, conf: "medium", detailHas: "; not priced: its license couldn't be read from the bundle's image"},
		{name: "directory last", mutate: func(ws *inventory.WorkSpace) {},
			wantFinding: true, conf: "medium", usd: 35, detailHas: "directory's last WorkSpaces", fixHas: "# a person's call: once its WorkSpaces are gone, deregister the directory"},
		{name: "free price note", mutate: func(ws *inventory.WorkSpace) {},
			wantFinding: true, conf: "medium", usd: 35, detailHas: "the us-east-1 price; other regions may differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := wsBase()
			tc.mutate(&ws)
			items := []inventory.WorkSpace{ws}
			items = append(items, tc.extra...)
			r := &inventory.Region{Name: "eu-west-1", WorkSpaces: &inventory.WorkSpaces{Items: items}}
			got := UnusedWorkSpaces(r, opts())
			switch {
			case tc.wantNothing:
				if len(got) != 0 || len(r.Unjudged) != 0 {
					t.Fatalf("wanted silence, got %+v unjudged %v", got, r.Unjudged)
				}
			case tc.wantUnjudged:
				if len(got) != 0 || !slices.Equal(r.Unjudged, []string{unusedWorkspace + "|" + ws.ID}) {
					t.Fatalf("wanted unjudged only, got %+v %v", got, r.Unjudged)
				}
			case tc.wantFinding:
				if len(got) != 1 || got[0].Key != ws.ID || len(r.Unjudged) != 0 {
					t.Fatalf("wanted one finding, got %+v %v", got, r.Unjudged)
				}
				if tc.conf != "" && got[0].Confidence != tc.conf {
					t.Errorf("confidence %q, want %q", got[0].Confidence, tc.conf)
				}
				if tc.usd > 0 {
					wantUSD(t, got[0], tc.usd)
				} else if got[0].Priced {
					t.Errorf("wanted unpriced, got %+v", got[0])
				}
				if tc.detailHas != "" && !strings.Contains(got[0].Detail, tc.detailHas) {
					t.Errorf("detail missing %q: %s", tc.detailHas, got[0].Detail)
				}
				if tc.fixHas != "" && !strings.Contains(strings.Join(got[0].Fix, "\n"), tc.fixHas) {
					t.Errorf("fix missing %q: %v", tc.fixHas, got[0].Fix)
				}
				if strings.Contains(got[0].Detail, "jane") || strings.Contains(got[0].Why, "jane") {
					t.Error("user name leaked into Detail/Why")
				}
			}
		})
	}
}

func TestUnusedWorkSpacesDirectoryOnlyWhenAllFlagged(t *testing.T) {
	a, b := wsBase(), wsBase()
	b.ID = "ws-2"
	b.LastSignIn = now.AddDate(0, 0, -5) // recent: unjudged, directory still has a live seat
	r := &inventory.Region{Name: "eu-west-1", WorkSpaces: &inventory.WorkSpaces{Items: []inventory.WorkSpace{a, b}}}
	got := UnusedWorkSpaces(r, opts())
	if len(got) != 1 || strings.Contains(got[0].Detail, "directory's last") {
		t.Fatalf("directory fee must wait until every seat is flagged: %+v", got)
	}
}
