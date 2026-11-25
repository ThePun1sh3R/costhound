//go:build !pro

package report

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

func assertFreeUpsell(t *testing.T, out string) {
	t.Helper()
	want := fmt.Sprintf("Pro adds %d more checks (", checks.Count(license.PlanPro))
	if !strings.Contains(out, want) {
		t.Errorf("public free upsell misses registry count (%d):\n%s", checks.Count(license.PlanPro), out)
	}
	if !strings.Contains(out, "more checks (usage-based idle resources, AWS Compute Optimizer and Cost Optimization Hub advice, extended-support cliffs, ...): https://costhound.dev") {
		t.Errorf("public free upsell misses the fixed Pro pitch:\n%s", out)
	}
	if strings.Contains(out, "-pro-preview") {
		t.Errorf("public free build must not invite -pro-preview:\n%s", out)
	}
}

// The free upsell's Pro count is the registry's PlanPro total, so a new check
// can't leave the pitch behind.
func TestFreeUpsellCountMatchesRegistry(t *testing.T) {
	var b strings.Builder
	sample().Text(&b, false)
	assertFreeUpsell(t, b.String())
	if checks.Count(license.PlanTeam) == 0 {
		t.Fatal("registry has no Team checks; Team upsell line is gated on that count")
	}
}
