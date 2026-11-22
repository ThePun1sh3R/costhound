package report

import (
	"math"

	"github.com/ThePun1sh3R/costhound/internal/checks"
)

func fnd(check, region, res string, usd float64) checks.Finding {
	return checks.Finding{Check: check, Region: region, Resource: res, MonthlyUSD: usd, Priced: usd > 0}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }
