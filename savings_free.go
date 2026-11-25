//go:build !pro

package main

import (
	"fmt"
	"os"
)

func runSavings(args []string) int {
	fmt.Fprintln(os.Stdout, "costhound Pro keeps a verified-savings ledger across scans so you can show what you fixed: https://costhound.dev")
	return 1
}
