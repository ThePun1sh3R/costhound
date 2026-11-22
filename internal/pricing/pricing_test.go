package pricing

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

func TestFixedPrices(t *testing.T) {
	if !near(PublicIPv4Monthly, 3.65) {
		t.Errorf("PublicIPv4Monthly = %v, want 3.65", PublicIPv4Monthly)
	}
}

// The Price List names two cache sizes differently from the API.
func TestAPICacheMonthly(t *testing.T) {
	b := Default()
	for size, hour := range map[string]float64{"0.5": 0.02, "1.6": 0.038, "6.1": 0.20, "237": 3.80} {
		if got, ok := b.APICacheMonthly(size); !ok || math.Abs(got-hour*HoursPerMonth) > 1e-9 {
			t.Errorf("%s GB: %v %v, want %v", size, got, ok, hour*HoursPerMonth)
		}
	}
	if _, ok := b.APICacheMonthly("999"); ok {
		t.Error("a size with no price is unpriced")
	}
}
