package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeygenAndDefaultKeyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	dir, err := defaultKeyDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "costhound")
	if dir != want {
		// Linux UserConfigDir = XDG_CONFIG_HOME; macOS uses HOME/Library/Application Support.
		if !strings.HasPrefix(dir, home) || !strings.HasSuffix(dir, "costhound") {
			t.Fatalf("defaultKeyDir = %q, want under %q", dir, home)
		}
	}

	var out bytes.Buffer
	if code := keygen(nil, &out); code != 0 {
		t.Fatalf("keygen default dir exit %d", code)
	}
	priv := filepath.Join(dir, "report-signing.key")
	pub := filepath.Join(dir, "report-signing.pub")
	if _, err := os.Stat(priv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pub); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(priv)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key mode = %v", st.Mode())
	}
	if !strings.Contains(out.String(), priv) || !strings.Contains(out.String(), "fingerprint") {
		t.Fatalf("stdout: %s", out.String())
	}
	if code := keygen(nil, &out); code == 0 {
		t.Fatal("second keygen must refuse overwrite")
	}

	custom := filepath.Join(home, "custom-keys")
	out.Reset()
	if code := keygen([]string{"-dir", custom}, &out); code != 0 {
		t.Fatalf("keygen -dir exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(custom, "report-signing.key")); err != nil {
		t.Fatal(err)
	}
}
