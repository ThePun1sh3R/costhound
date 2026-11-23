package checks

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

func pcaCA(status, typ, mode string, createdAge, changeAge, notAfterAge int) inventory.PrivateCA {
	arn := "arn:aws:acm-pca:eu-west-1:123456789012:certificate-authority/" + status + "-" + typ
	ca := inventory.PrivateCA{
		ARN: arn, ID: status + "-" + typ, Status: status, Type: typ, UsageMode: mode,
		CommonName: "Example CA", PolicyRead: true,
	}
	if createdAge >= 0 {
		ca.Created = now.AddDate(0, 0, -createdAge)
	}
	if changeAge >= 0 {
		ca.LastStateChange = now.AddDate(0, 0, -changeAge)
	}
	if notAfterAge >= 0 {
		ca.NotAfter = now.AddDate(0, 0, -notAfterAge)
	}
	return ca
}

func TestUnusedPrivateCAs(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ca             inventory.PrivateCA
		acmRead, named bool
		anchorsRead    bool
		anchored       bool
		wantFinding    bool
		wantConf       string
		wantUSD        float64
		unjudged       bool
		detailHas      string
		evidenceHas    string
		fixHas         string
		fixLacks       string
	}{
		{name: "active", ca: pcaCA("ACTIVE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1), unjudged: true},
		{name: "creating", ca: pcaCA("CREATING", "ROOT", "GENERAL_PURPOSE", 40, -1, -1), unjudged: true},
		{name: "unknown status", ca: pcaCA("SOMETHING", "ROOT", "GENERAL_PURPOSE", 40, -1, -1), unjudged: true},
		{name: "young pending", ca: pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 10, -1, -1), unjudged: true},
		{name: "deleted", ca: pcaCA("DELETED", "ROOT", "GENERAL_PURPOSE", 40, -1, -1)},
		{name: "failed", ca: pcaCA("FAILED", "ROOT", "GENERAL_PURPOSE", 40, -1, -1)},
		{name: "pending", ca: pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1),
			wantFinding: true, wantConf: "high", wantUSD: pricing.PCAGeneralPurposeMonthly, detailHas: "never got a CA certificate", anchorsRead: true, fixHas: "AWS keeps the CA restorable for 30 days"},
		{name: "expired old", ca: pcaCA("EXPIRED", "SUBORDINATE", "GENERAL_PURPOSE", 40, 40, 40),
			wantFinding: true, wantConf: "high", wantUSD: pricing.PCAGeneralPurposeMonthly, detailHas: "expired on", anchorsRead: true, fixHas: "AWS keeps the CA restorable for 30 days"},
		{name: "expired recent", ca: pcaCA("EXPIRED", "SUBORDINATE", "GENERAL_PURPOSE", 40, 10, 10),
			wantFinding: true, wantConf: "medium", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true},
		{name: "disabled short-lived", ca: pcaCA("DISABLED", "SUBORDINATE", "SHORT_LIVED_CERTIFICATE", 40, 10, -1),
			wantFinding: true, wantConf: "high", wantUSD: pricing.PCAShortLivedMonthly, anchorsRead: true, acmRead: true, fixHas: "can't be revoked after the delete"},
		{name: "disabled root", ca: pcaCA("DISABLED", "ROOT", "GENERAL_PURPOSE", 40, 10, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true},
		{name: "disabled subordinate medium", ca: pcaCA("DISABLED", "SUBORDINATE", "GENERAL_PURPOSE", 500, 400, -1),
			wantFinding: true, wantConf: "medium", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true, acmRead: true, evidenceHas: "no certificate in this account names it as its issuer"},
		{name: "disabled subordinate named", ca: pcaCA("DISABLED", "SUBORDINATE", "GENERAL_PURPOSE", 500, 400, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true, acmRead: true, named: true},
		{name: "disabled subordinate unread acm", ca: pcaCA("DISABLED", "SUBORDINATE", "GENERAL_PURPOSE", 500, 400, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true},
		{name: "disabled subordinate young", ca: pcaCA("DISABLED", "SUBORDINATE", "GENERAL_PURPOSE", 40, 30, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true, acmRead: true},
		{name: "shared cap", ca: func() inventory.PrivateCA {
			ca := pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1)
			ca.Shared = true
			return ca
		}(), wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true, detailHas: "never got",
			evidenceHas: "AWS Private CA GetPolicy: a resource policy is attached"},
		{name: "anchored cap", ca: pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true, anchored: true,
			evidenceHas: "IAM Roles Anywhere ListTrustAnchors: a trust anchor names this CA"},
		{name: "unread anchors cap", ca: pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1),
			wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly},
		{name: "unread policy cap", ca: func() inventory.PrivateCA {
			ca := pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 40, -1, -1)
			ca.PolicyRead = false
			return ca
		}(), wantFinding: true, wantConf: "low", wantUSD: pricing.PCAGeneralPurposeMonthly, anchorsRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &inventory.Region{Name: "eu-west-1", PCA: &inventory.PCA{
				CAs: []inventory.PrivateCA{tc.ca}, AnchorsRead: tc.anchorsRead,
				Anchored: map[string]bool{}, ACMRead: tc.acmRead, ACMNamed: map[string]bool{},
			}}
			if tc.anchored {
				r.PCA.Anchored[tc.ca.ARN] = true
			}
			if tc.named {
				r.PCA.ACMNamed[tc.ca.ARN] = true
			}
			got := UnusedPrivateCAs(r, opts())
			if tc.unjudged || !tc.wantFinding {
				if len(got) != 0 {
					t.Fatalf("wanted no finding, got %+v", got)
				}
				if tc.unjudged && !slices.Contains(r.Unjudged, unusedPrivateCA+"|"+tc.ca.ARN) {
					t.Fatalf("wanted Unjudged key, got %v", r.Unjudged)
				}
				if !tc.unjudged && len(r.Unjudged) != 0 {
					t.Fatalf("DELETED/FAILED must not park: %v", r.Unjudged)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("wanted one finding, got %+v", got)
			}
			f := withDefaults(got[0])
			if f.Key != tc.ca.ARN || f.Confidence != tc.wantConf {
				t.Fatalf("key/conf: %+v", f)
			}
			wantUSD(t, f, tc.wantUSD)
			if !strings.Contains(f.Detail, "(the us-east-1 price; other regions may differ)") {
				t.Errorf("free price note missing: %s", f.Detail)
			}
			if tc.detailHas != "" && !strings.Contains(f.Detail, tc.detailHas) {
				t.Errorf("detail %q missing %q", f.Detail, tc.detailHas)
			}
			if ev := strings.Join(f.Evidence, "\n"); tc.evidenceHas != "" && !strings.Contains(ev, tc.evidenceHas) {
				t.Errorf("evidence %q missing %q", ev, tc.evidenceHas)
			}
			fix := strings.Join(f.Fix, "\n")
			if tc.fixHas != "" && !strings.Contains(fix, tc.fixHas) {
				t.Errorf("fix %q missing %q", fix, tc.fixHas)
			}
			if tc.fixLacks != "" && strings.Contains(fix, tc.fixLacks) {
				t.Errorf("fix %q has %q", fix, tc.fixLacks)
			}
			if !strings.Contains(f.Resource, tc.ca.ID) || !strings.Contains(f.Resource, "Example CA") {
				t.Errorf("resource: %q", f.Resource)
			}
		})
	}
}

func TestUnusedPrivateCAsNilPCA(t *testing.T) {
	if UnusedPrivateCAs(&inventory.Region{Name: "eu-west-1"}, opts()) != nil {
		t.Fatal("nil PCA must produce nothing")
	}
}

func TestUnusedPrivateCAsShortLivedPrice(t *testing.T) {
	ca := pcaCA("PENDING_CERTIFICATE", "ROOT", "SHORT_LIVED_CERTIFICATE", 40, -1, -1)
	r := &inventory.Region{Name: "eu-west-1", PCA: &inventory.PCA{
		CAs: []inventory.PrivateCA{ca}, AnchorsRead: true, Anchored: map[string]bool{}, ACMNamed: map[string]bool{},
	}}
	got := UnusedPrivateCAs(r, opts())
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	wantUSD(t, got[0], pricing.PCAShortLivedMonthly)
	if !strings.Contains(got[0].Detail, "short-lived mode") {
		t.Errorf("detail: %s", got[0].Detail)
	}
}

func TestUnusedPrivateCAsAgeBoundary(t *testing.T) {
	ca := pcaCA("PENDING_CERTIFICATE", "ROOT", "GENERAL_PURPOSE", 0, -1, -1)
	ca.Created = now.Add(-30*24*time.Hour + time.Second)
	r := &inventory.Region{Name: "eu-west-1", PCA: &inventory.PCA{CAs: []inventory.PrivateCA{ca}, AnchorsRead: true}}
	if got := UnusedPrivateCAs(r, opts()); len(got) != 0 || !slices.Contains(r.Unjudged, unusedPrivateCA+"|"+ca.ARN) {
		t.Fatalf("just under 30 days must park: %+v %v", got, r.Unjudged)
	}
}
