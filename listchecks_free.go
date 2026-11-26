//go:build !pro

package main

// registeredChecks is the free/source build: AWS free checks only. Fix bundles
// and Azure/GCP registries are paid-only, so FixScript is always false here.
func registeredChecks() []checkRow {
	return awsCheckRows(true)
}

func awsFixScript(string) bool { return false }
