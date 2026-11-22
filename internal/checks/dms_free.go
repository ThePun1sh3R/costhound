//go:build !pro

package checks

import (
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

func dmsMetricProof(*inventory.Region, Options, inventory.DMSInstance, *Finding) bool { return true }

func dmsHour(_ *inventory.Region, _ Options, i inventory.DMSInstance) (hour float64, regional, read bool) {
	return pricing.DMSHour(i.Class, *i.MultiAZ), false, true
}
