//go:build !pro

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestSavingsSourceBuildMessage(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code := runSavings(nil)
	_ = w.Close()
	os.Stdout = stdout
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	s := string(got)
	if !strings.Contains(s, "verified-savings ledger") || !strings.Contains(s, "https://costhound.dev") {
		t.Fatalf("message: %q", s)
	}
}
