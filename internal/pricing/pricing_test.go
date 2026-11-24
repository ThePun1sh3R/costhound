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

func TestEFSMonthlyRates(t *testing.T) {
	const gib = int64(1 << 30)
	cases := []struct {
		name          string
		std, ia, arch int64
		prov          float64
		oneZone       bool
		mode          string
		want          float64
	}{
		{"regional bursting", 100 * gib, 200 * gib, 0, 0, false, "bursting", 100*0.30 + 200*0.025},
		{"regional provisioned", 10 * gib, 10 * gib, 0, 5, false, "provisioned", 10*0.30 + 10*0.025 + 5*6},
		{"regional elastic IA", 0, 100 * gib, 0, 0, false, "elastic", 100 * 0.016},
		{"one zone standard+IA", 50 * gib, 20 * gib, 0, 0, true, "bursting", 50*0.16 + 20*0.0133},
		{"one zone ignores elastic", 0, 10 * gib, 0, 0, true, "elastic", 10 * 0.0133},
		{"archive", 0, 0, 100 * gib, 0, false, "bursting", 100 * 0.008},
	}
	for _, c := range cases {
		if got := EFSMonthly(c.std, c.ia, c.arch, c.prov, c.oneZone, c.mode); !near(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestLogsStoredRate(t *testing.T) {
	rate, assumed := LogsStoredRate("", 0.03)
	if !near(rate, 0.03) || !assumed {
		t.Errorf("empty class: %v %v", rate, assumed)
	}
	rate, assumed = LogsStoredRate("INFREQUENT_ACCESS", 0.03)
	if !near(rate, 0.018) || assumed {
		t.Errorf("IA: %v %v", rate, assumed)
	}
	rate, assumed = LogsStoredRate("ARCHIVE_INSTANT_ACCESS", 0.03)
	if !near(rate, 0.006) || assumed {
		t.Errorf("archive: %v %v", rate, assumed)
	}
	rate, assumed = LogsStoredRate("STANDARD", 0.04)
	if !near(rate, 0.04) || assumed {
		t.Errorf("standard uses book rate: %v %v", rate, assumed)
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
