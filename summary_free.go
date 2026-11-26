//go:build !pro

package main

import (
	"fmt"
	"os"
)

func runSummary(args []string) int {
	fmt.Fprintln(os.Stdout, "costhound Pro shows one view of the latest scan of every AWS, Azure and GCP scope already saved on this machine: https://costhound.dev")
	return 1
}
