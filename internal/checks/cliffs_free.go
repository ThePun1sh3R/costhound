//go:build !pro

package checks

import "github.com/ThePun1sh3R/costhound/internal/inventory"

// The free build has no usage data, so no calendar.
func cliffs(*inventory.Region, Options) []Cliff { return nil }
