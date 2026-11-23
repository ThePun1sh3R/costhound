package main

import "runtime/debug"

// A `go install github.com/ThePun1sh3R/costhound@vX.Y.Z` build gets no
// -ldflags version, so it said "dev". Take the module's version instead.
func init() {
	if version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		version = moduleVersion(bi.Main.Version)
	}
}

// moduleVersion keeps "dev" for a local build ("(devel)" or empty).
func moduleVersion(v string) string {
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return v
}
