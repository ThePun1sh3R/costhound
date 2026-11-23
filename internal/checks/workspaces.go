package checks

import (
	"fmt"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

const unusedWorkspace = "unused-workspace"

// UnusedWorkSpaces finds AlwaysOn and AutoStop WorkSpaces nobody has signed
// in to for max(30, -usage-days) days by WorkSpaces' own connection record
// (Pro also catches never-connected seats with a CloudWatch age proof).
func UnusedWorkSpaces(r *inventory.Region, o Options) []Finding {
	w := r.WorkSpaces
	if w == nil {
		return nil
	}
	window := max(inventory.WorkSpacesIdleDays, o.UsageDays)
	liveByDir := map[string]int{}
	for _, ws := range w.Items {
		if ws.ID == "" || ws.State == "TERMINATED" || ws.State == "TERMINATING" {
			continue
		}
		if ws.DirectoryID != "" {
			liveByDir[ws.DirectoryID]++
		}
	}
	var flagged []inventory.WorkSpace
	var out []Finding
	for _, ws := range w.Items {
		if ws.ID == "" {
			continue
		}
		if ws.State == "TERMINATED" || ws.State == "TERMINATING" {
			continue
		}
		f, ok := unusedWorkspaceFinding(r, o, ws, window)
		if !ok {
			r.Unjudged = append(r.Unjudged, unusedWorkspace+"|"+ws.ID)
			continue
		}
		flagged = append(flagged, ws)
		out = append(out, f)
	}
	byolTotal, byolFlagged := 0, 0
	for _, ws := range w.Items {
		if ws.ID == "" || ws.State == "TERMINATED" || ws.State == "TERMINATING" || !ws.BYOL {
			continue
		}
		byolTotal++
	}
	for _, ws := range flagged {
		if ws.BYOL {
			byolFlagged++
		}
	}
	remainBYOL := byolTotal - byolFlagged
	flaggedByDir := map[string]int{}
	for _, ws := range flagged {
		if ws.DirectoryID != "" {
			flaggedByDir[ws.DirectoryID]++
		}
	}
	for i := range out {
		f := &out[i]
		ws := flagged[i]
		if ws.BYOL && remainBYOL < 50 {
			f.Confidence = "low"
			f.MonthlyUSD, f.Priced = 0, false
			if before, _, ok := strings.Cut(f.Detail, "; billed $"); ok {
				f.Detail = before
			}
			if before, _, ok := strings.Cut(f.Detail, "; not priced:"); ok {
				f.Detail = before
			}
			f.Detail += fmt.Sprintf("; not priced: AWS can bill BYOL for at least 50 WorkSpaces a region (most bundles; linked accounts count together), and this account would keep %d here, so the terminate may save nothing", remainBYOL)
		}
		if ws.DirectoryID != "" && flaggedByDir[ws.DirectoryID] == liveByDir[ws.DirectoryID] {
			f.Detail += "; these are the directory's last WorkSpaces: if it's a Simple AD or AD Connector, it starts billing 30 days after they go ($36.50 to $109.50 a month in US East), unless it's deleted too"
			f.Fix = append(f.Fix,
				"# a person's call: once its WorkSpaces are gone, deregister the directory, then delete it only if nothing else uses it (a deleted Simple AD can't be recovered)",
				fmt.Sprintf("# aws workspaces deregister-workspace-directory --region %s --directory-id %s", r.Name, ws.DirectoryID),
				fmt.Sprintf("# aws ds delete-directory --region %s --directory-id %s", r.Name, ws.DirectoryID),
			)
		}
	}
	return out
}

func unusedWorkspaceFinding(r *inventory.Region, o Options, ws inventory.WorkSpace, window int) (Finding, bool) {
	if ws.State != "AVAILABLE" && ws.State != "STOPPED" {
		return Finding{}, false
	}
	if ws.RunningMode != "ALWAYS_ON" && ws.RunningMode != "AUTO_STOP" || ws.Standby {
		return Finding{}, false
	}
	if !ws.StatusRead {
		return Finding{}, false
	}
	if ws.ConnectionState == "CONNECTED" {
		return Finding{}, false
	}

	never := ws.LastSignIn.IsZero()
	var ageDays int
	var since string
	var cwEvidence string
	if never {
		ok, days, evidence := workspaceNeverConnected(r, o, ws, window)
		if !ok {
			return Finding{}, false
		}
		ageDays, cwEvidence = days, evidence
		since = "never"
	} else {
		ageDays = int(o.Now.Sub(ws.LastSignIn).Hours() / 24)
		if ageDays < window {
			return Finding{}, false
		}
		since = ws.LastSignIn.UTC().Format("2006-01-02")
	}

	signin := "last sign-in " + since
	if never {
		signin = "no sign-in on record"
	}
	res := ws.ID
	if ws.UserName != "" {
		res += " (" + ws.UserName + ")"
	}
	checked := "unknown"
	if !ws.ConnectionChecked.IsZero() {
		checked = ws.ConnectionChecked.UTC().Format("2006-01-02")
	}
	f := Finding{
		Check: unusedWorkspace, Region: r.Name, Resource: res, Key: ws.ID,
		Why: "a seat kept for a returning user or for emergencies, or one used only over RDP or SSH, which WorkSpaces may not record, looks the same",
		Evidence: []string{fmt.Sprintf("WorkSpaces DescribeWorkspacesConnectionStatus: %s, %s, checked %s",
			ws.ConnectionState, signin, checked)},
		Facts: facts("workspace", ws.ID, "running_mode", ws.RunningMode, "compute_type", ws.ComputeType,
			"os", ws.OS, "state", ws.State, "last_signin", since, "user", ws.UserName,
			"computer", ws.ComputerName, "ip", ws.IP, "directory", ws.DirectoryID,
			"window_days", itoa(window)),
		Fix: workspaceFix(r.Name, ws),
	}
	if ageDays >= 90 {
		f.Confidence = "high"
	} else {
		f.Confidence = "medium"
	}
	if never {
		f.Detail = fmt.Sprintf("%s WorkSpace (%s, %s), state %s: nobody has ever signed in (%d days since CloudWatch first saw it)",
			ws.RunningMode, ws.ComputeType, ws.OS, ws.State, ageDays)
		if cwEvidence != "" {
			f.Evidence = append(f.Evidence, cwEvidence)
		}
	} else {
		f.Detail = fmt.Sprintf("%s WorkSpace (%s, %s), state %s: nobody has signed in since %s (%d days)",
			ws.RunningMode, ws.ComputeType, ws.OS, ws.State, since, ageDays)
	}
	usd, regional, read := workspaceMonthly(r, o, ws)
	switch {
	case !read:
		f.Detail += "; not priced: the region's WorkSpaces prices couldn't be read"
	case ws.LicenseUnread:
		f.Detail += "; not priced: its license couldn't be read from the bundle's image"
	case usd > 0:
		f.MonthlyUSD, f.Priced = usd, true
		f.Detail += fmt.Sprintf("; billed $%.2f a month", usd)
		if ws.RunningMode == "AUTO_STOP" {
			f.Detail += ", its fixed AutoStop fee"
		}
		f.Detail += "; a terminate saves it from next month"
		if !regional {
			f.Detail += " (the us-east-1 price; other regions may differ)"
		}
	default:
		f.Detail += "; not priced: no monthly price for its bundle, OS, license and volume sizes"
	}
	return f, true
}

// workspaceBuiltin is the built-in us-east-1 fee: Windows with the license
// included only. Linux, BYOL or an unread license is unpriced (0).
func workspaceBuiltin(ws inventory.WorkSpace) float64 {
	if !strings.HasPrefix(ws.OS, "WINDOWS") || ws.BYOL || ws.LicenseUnread {
		return 0
	}
	return pricing.WorkspaceMonthly(ws.RunningMode, ws.ComputeType, ws.RootGiB, ws.UserGiB)
}

func workspaceFix(region string, ws inventory.WorkSpace) []string {
	var fix []string
	if ws.RunningMode == "ALWAYS_ON" {
		fix = append(fix,
			"# to keep the desktop: switch it to AutoStop; the lower monthly fee starts next month, and hours of use bill on top",
			fmt.Sprintf("# aws workspaces modify-workspace-properties --region %s --workspace-id %s --workspace-properties RunningMode=AUTO_STOP", region, ws.ID),
		)
	}
	return append(fix,
		"# a person's call: a terminate deletes the desktop and its user data for good",
		fmt.Sprintf("# aws workspaces terminate-workspaces --region %s --terminate-workspace-requests WorkspaceId=%s", region, ws.ID),
	)
}
