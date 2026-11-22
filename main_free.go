//go:build !pro

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/owner"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

// This file stands in for the paid features in source builds of the
// open-source tree. The official release is built with -tags pro.

const proOnly = "is in the official costhound release (Pro/Team), not in builds from source"

func mapTerraform(*report.Report, []string) error {
	return errors.New("Terraform state mapping (-tf-state) " + proOnly)
}

func writeICS(*report.Report, string) error {
	return errors.New("the cost-cliff calendar (-ics) " + proOnly)
}

func signReport(*report.Report, string, string) error {
	return errors.New("signed reports (-sign) " + proOnly)
}

func redactReport(*report.Report, string, string) (*report.Report, error) {
	return nil, errors.New("redacted reports (-redact) " + proOnly)
}

func teamActions(ctx context.Context, rep *report.Report, hook string, failOver float64, failOnNew bool, failOnUnowned float64, scope string) int {
	if hook != "" || failOver > 0 || failOnNew || failOnUnowned > 0 {
		fmt.Fprintln(os.Stderr, "costhound: Slack posting and the CI gate "+proOnly+".")
	}
	return 0
}

func writeHTML(rep *report.Report, path string) error {
	fmt.Fprintln(os.Stderr, "costhound: the HTML report "+proOnly+"; printing the text report only.")
	return nil
}

func keepHistory(rep *report.Report, dir string, complete bool) {}

func stampCreated(*inventory.Region, []checks.Finding) {}

func findCreators(context.Context, aws.Config, *report.Report, owner.Keys) {
	fmt.Fprintln(os.Stderr, "costhound: -creators (who created a resource with no owner tag) "+proOnly+".")
}

func scanOrganization(ctx context.Context, cfg aws.Config, self string, lic license.Status, role, only string, st settings) (*report.Report, error) {
	return nil, errors.New("scanning a whole Organization " + proOnly)
}

func serve(args []string) int {
	fmt.Fprintln(os.Stderr, "costhound: the dashboard (costhound serve) "+proOnly+".")
	return 1
}

func planBundle(args []string) int {
	fmt.Fprintln(os.Stderr, "costhound: fix bundles (costhound plan) "+proOnly+".")
	return 1
}

// savedReports: source builds keep no history; use explain -from report.json.
func savedReports(dir string) []*report.Report { return nil }

// acceleratorUsage: paid plans read the accelerators' traffic.
func acceleratorUsage(context.Context, aws.Config, *inventory.Region, checks.Options) {}

// youngZonesOut: -creators is part of the paid plans.
func youngZonesOut(_ context.Context, _ aws.Config, _ checks.Options, _ *inventory.Region, fs []checks.Finding) []checks.Finding {
	return fs
}

func globalIndexes(fs []checks.Finding, _ []string) ([]checks.Finding, []string) { return fs, nil }

// loadPaid: source builds read no usage, prices or commitments.
func loadPaid(context.Context, aws.Config, *inventory.Region, checks.Options, string, *pricing.Client) {
}

// paidAccount: source builds have no account-wide paid findings.
func paidAccount(_ context.Context, _ aws.Config, _ settings, findings []checks.Finding, cliffs []checks.Cliff, skipped []inventory.Skip) ([]checks.Finding, []checks.Cliff, []inventory.Skip) {
	return findings, cliffs, skipped
}

// orgPlans: source builds don't scan Organizations.
type orgPlans struct{}
