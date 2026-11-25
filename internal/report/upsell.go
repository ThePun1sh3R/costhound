//go:build !pro

package report

import (
	"fmt"
	"io"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

// writeFreeUpsell is the public free build's closing pitch: how many Pro
// checks exist. Official (pro-tag) builds replace this with a -pro-preview invite.
func writeFreeUpsell(w io.Writer, _ *Report) {
	if n := checks.Count(license.PlanPro); n > 0 {
		fmt.Fprintf(w, "Pro adds %d more checks (usage-based idle resources, AWS Compute Optimizer and Cost Optimization Hub advice, extended-support cliffs, ...): %s\n", n, license.RenewURL)
	}
	writeFreeUpsellTail(w)
}

func writeFreeUpsellTail(w io.Writer) {
	if checks.Count(license.PlanTeam) > 0 {
		fmt.Fprintf(w, "Team adds Organization-wide reports, Slack and CI gates.\n")
	}
	fmt.Fprint(w, "costhound Pro also writes each fix as a script to review and run, with a backup where AWS allows one, and a rollback (costhound plan).\n\n")
}
