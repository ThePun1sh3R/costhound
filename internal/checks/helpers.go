package checks

import (
	"fmt"

	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

const bytesPerGiB = float64(1 << 30)

func money(v float64) string {
	return "$" + strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", v), "0"), ".0")
}

func listSome(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" and %d more", len(xs)-n)
}

// pb is the region's price book: real regional prices on paid plans, the
// built-in us-east-1 list otherwise.
func pb(r *inventory.Region) *pricing.Book {
	if r != nil && r.Prices != nil {
		return r.Prices
	}
	return pricing.Default()
}
