package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRedactFullPath(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "full.json")
	if err := checkRedactFullPath(ok); err != nil {
		t.Fatalf("writable new path: %v", err)
	}
	if _, err := os.Stat(ok); !os.IsNotExist(err) {
		t.Fatalf("pre-check must not create the file: %v", err)
	}

	existing := filepath.Join(dir, "exists.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkRedactFullPath(existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing path: %v", err)
	}

	missing := filepath.Join(dir, "no-such-dir", "full.json")
	if err := checkRedactFullPath(missing); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing folder: %v", err)
	}

	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	if f, err := os.Create(filepath.Join(ro, ".probe")); err == nil {
		f.Close()
		os.Remove(f.Name())
		// e.g. running as root: directory mode does not block writes
	} else if err := checkRedactFullPath(filepath.Join(ro, "full.json")); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("unwritable folder: %v", err)
	}
}
