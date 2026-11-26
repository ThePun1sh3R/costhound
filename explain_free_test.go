//go:build !pro

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainAzureGCPFreeBuildMessage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "azure.json")
	body := []byte(`{"tool":"costhound","cloud":"azure","tenant_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","subscription_ids":["11111111-1111-1111-1111-111111111111"],"findings":[{"id":"ch_azfree01","check":"azure-unattached-disk","region":"eastus","resource":"disk"}]}`)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	code := explain([]string{"ch_azfree01", "-from", p}, &out)
	w.Close()
	os.Stderr = old
	var errBuf bytes.Buffer
	_, _ = errBuf.ReadFrom(r)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "Azure and GCP are Pro/Team features") {
		t.Fatalf("free message missing:\n%s", errBuf.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout should be empty: %s", out.String())
	}
}
