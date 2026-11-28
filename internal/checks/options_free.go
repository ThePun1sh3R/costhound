//go:build !pro

package checks

import "github.com/ThePun1sh3R/costhound/internal/inventory"

func regionOptions(*inventory.Region, Options) []Option { return nil }
