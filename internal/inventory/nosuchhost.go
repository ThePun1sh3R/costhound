package inventory

import (
	"errors"
	"net"
	"strings"
)

// NoSuchHost is ShortError's stable form of a DNS NXDOMAIN on an AWS endpoint.
const NoSuchHost = "no such host"

// IsNoSuchHost reports whether err is (or wraps) a DNS failure to resolve a
// host: the regional endpoint for a service AWS doesn't offer there, or a
// broken resolver. Prefer errors.As on *net.DNSError; the string form catches
// SDK wraps that lose the typed DNS error.
func IsNoSuchHost(err error) bool {
	if err == nil {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such host")
}

// ServiceLabel is the short product name for an API call ("apprunner:ListServices"
// → "App Runner"), for "aren't offered here" lines.
func ServiceLabel(what string) string {
	svc, _, _ := strings.Cut(what, ":")
	if name, ok := serviceLabels[svc]; ok {
		return name
	}
	if svc == "" {
		return what
	}
	return svc
}

// serviceLabels maps the API service id (the part before ':' in a skip's What)
// to the name printed in not-offered summaries. Unknown ids print as-is.
var serviceLabels = map[string]string{
	"apprunner":     "App Runner",
	"dlm":           "Data Lifecycle Manager",
	"appstream":     "AppStream",
	"docdb-elastic": "DocumentDB Elastic",
	"glue":          "Glue",
	"workspaces":    "WorkSpaces",
	"kendra":        "Kendra",
	"comprehend":    "Comprehend",
	"lightsail":     "Lightsail",
	"mwaa":          "MWAA",
	"proton":        "Proton",
	"nimble":        "Nimble Studio",
	"gamesparks":    "GameSparks",
}
