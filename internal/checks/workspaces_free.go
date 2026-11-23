//go:build !pro

package checks

import (
	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func workspaceNeverConnected(*inventory.Region, Options, inventory.WorkSpace, int) (ok bool, ageDays int, evidence string) {
	return false, 0, ""
}

func workspaceMonthly(_ *inventory.Region, _ Options, ws inventory.WorkSpace) (usd float64, regional, read bool) {
	return workspaceBuiltin(ws), false, true
}
