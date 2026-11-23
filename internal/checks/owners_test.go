package checks

import (
	"regexp"
	"strings"
	"testing"
)

// Each check either names the one resource its findings are about (so its
// owner can be found) or says why it can't.
func TestEveryCheckSaysWhichResource(t *testing.T) {
	for _, c := range All {
		_, named := tagRefs[c.ID]
		_, none := noTagRef[c.ID]
		n := 0
		for _, b := range []bool{named, none, selfTagRef[c.ID]} {
			if b {
				n++
			}
		}
		if n != 1 {
			t.Errorf("check %s must be in exactly one of tagRefs, selfTagRef or noTagRef (owners.go)", c.ID)
		}
	}
	for id := range tagRefs {
		if Title(id) == id {
			t.Errorf("tagRefs names unknown check %s", id)
		}
	}
}

var refShape = regexp.MustCompile(`^[a-z0-9-]+:([a-z0-9-]+[/:])?[^ /:][^ ]*$`)

func TestFindingsNameTheirResource(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Run(fixture(), teamOpts()) {
		_, named := tagRefs[f.Check]
		named = named || selfTagRef[f.Check]
		switch {
		case !named && f.TagRef != "":
			t.Errorf("%s sums up several resources but has TagRef %q", f.Check, f.TagRef)
		case named && f.TagRef == "" && f.Check == "commitments-expiring" && (f.Facts["kind"] == "OpenSearch" || f.Facts["kind"] == "Redshift"):
			// OpenSearch and Redshift reservations have no ARN (service authorization reference).
		case named && f.TagRef == "" && f.Key != "small-never-expiring-log-groups" && !strings.HasPrefix(f.Key, "amis-of-"):
			t.Errorf("%s %s: no TagRef, so its owner can't be found", f.Check, f.Resource)
		case f.TagRef != "" && !refShape.MatchString(f.TagRef):
			t.Errorf("%s: TagRef %q isn't an ARN after the account", f.Check, f.TagRef)
		}
		if f.TagRef != "" {
			seen[f.Check] = true
		}
	}
	for id := range tagRefs {
		if c := find(id); c.Run != nil && !seen[id] {
			t.Errorf("no fixture finding of %s has a TagRef", id)
		}
	}
}

func find(id string) Check {
	for _, c := range All {
		if c.ID == id {
			return c
		}
	}
	return Check{}
}

// The resources whose ID isn't the finding's label.
func TestTagRefsFromFacts(t *testing.T) {
	for _, c := range []struct {
		f    Finding
		want string
	}{
		{Finding{Check: "idle-ip", Resource: "52.1.2.3 (web)", Facts: map[string]string{"allocation": "eipalloc-0abc"}}, "ec2:elastic-ip/eipalloc-0abc"},
		{Finding{Check: "quiet-load-balancer", Resource: "web", Facts: map[string]string{"lb_arn": "arn:aws:elasticloadbalancing:eu-west-1:1:loadbalancer/app/web/50dc"}}, "elasticloadbalancing:loadbalancer/app/web/50dc"},
		{Finding{Check: "disabled-kms-key", Resource: "alias/old", Key: "k1", Facts: map[string]string{"key_arn": "arn:aws:kms:eu-west-1:1:key/k1"}}, "kms:key/k1"},
		{Finding{Check: "unused-private-ca", Resource: "ca-1 (Example)", Facts: map[string]string{"ca": "arn:aws:acm-pca:eu-west-1:1:certificate-authority/ca-1"}}, "acm-pca:certificate-authority/ca-1"},
		{Finding{Check: "idle-msk", Resource: "events", Facts: map[string]string{"cluster_arn": "arn:aws:kafka:eu-west-1:1:cluster/events/0d2f"}}, "kafka:cluster/events/0d2f"},
		{Finding{Check: "idle-elasticache", Resource: "sessions", Facts: map[string]string{"cache": "sessions", "replication_group": "true"}}, "elasticache:replicationgroup:sessions"},
		{Finding{Check: "idle-elasticache", Resource: "memo", Facts: map[string]string{"cache": "memo", "replication_group": "false"}}, "elasticache:cluster:memo"},
		{Finding{Check: "log-retention", Resource: "/app/web"}, "logs:log-group:/app/web"},
		{Finding{Check: "log-retention", Resource: "3 smaller log group(s)", Key: "small-never-expiring-log-groups"}, ""},
		{Finding{Check: "unattached-ebs", Resource: "vol-0abc (old-data)"}, "ec2:volume/vol-0abc"},
		{Finding{Check: "lambda-x86", Resource: "2 function(s) on x86_64", Key: "lambda-on-x86"}, ""},
	} {
		if got := withDefaults(c.f).TagRef; got != c.want {
			t.Errorf("%s %q: TagRef %q, want %q", c.f.Check, c.f.Resource, got, c.want)
		}
	}
}
