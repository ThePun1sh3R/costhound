//go:build !pro

package checks

import (
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

func pcaMonthly(_ *inventory.Region, _ Options, mode string) (usd float64, regional, read bool) {
	return pricing.PCAMonthly(mode), false, true
}
