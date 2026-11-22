//go:build !pro

package checks

import (
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// regionalRI: the free plan doesn't read reservations.
func regionalRI(*inventory.Region, string, time.Time) bool { return false }

// crUsage: the free plan reads no usage: what's free now, unproven.
func crUsage(_ *inventory.Region, cr inventory.CapacityReservation, _ time.Duration, _ int) (unused int, proof, young bool) {
	return cr.Available, false, false
}

// crHour: the free plan has the built-in list prices only.
func crHour(*inventory.Region, inventory.CapacityReservation) (float64, bool) { return 0, false }
