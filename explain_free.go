//go:build !pro

package main

import (
	"fmt"
	"io"
	"os"
)

func historyDirLabel(dir string) string {
	if dir != "" {
		return dir
	}
	return "<config dir>/costhound/runs"
}

// Free/source builds have no Azure/GCP fix bundles.
func explainFixHint(string) string { return "" }

const cloudExplainProOnly = "costhound: Azure and GCP are Pro/Team features, not in builds from source."

// explainCloudFromJSON: source builds cannot explain Azure/GCP findings.
func explainCloudFromJSON(_ []byte, _ string, _ io.Writer) int {
	fmt.Fprintln(os.Stderr, cloudExplainProOnly)
	return 2
}

// explainCloudFromHistory: source builds keep no Azure/GCP history. Return -1
// so an unknown AWS ID still gets the shared "not found" message.
func explainCloudFromHistory(string, string, io.Writer) int {
	return -1
}
