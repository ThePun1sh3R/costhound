package main

import "testing"

func TestModuleVersion(t *testing.T) {
	for in, want := range map[string]string{"": "dev", "(devel)": "dev", "v0.2.1": "v0.2.1"} {
		if got := moduleVersion(in); got != want {
			t.Errorf("moduleVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
