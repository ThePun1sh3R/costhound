package checks

import (
	"fmt"
	"strings"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

const unusedPrivateCA = "unused-private-ca"

// UnusedPrivateCAs finds Private CAs that still bill while they can't issue
// (never certified, expired or disabled) and are older than 30 days, AWS's
// free-trial window.
func UnusedPrivateCAs(r *inventory.Region, o Options) []Finding {
	if r.PCA == nil {
		return nil
	}
	var out []Finding
	for _, ca := range r.PCA.CAs {
		if ca.ARN == "" {
			continue
		}
		switch ca.Status {
		case "DELETED", "FAILED":
			continue
		case "ACTIVE", "CREATING":
			r.Unjudged = append(r.Unjudged, unusedPrivateCA+"|"+ca.ARN)
			continue
		case "PENDING_CERTIFICATE", "EXPIRED", "DISABLED":
			if ca.Created.IsZero() || o.Now.Sub(ca.Created) < 30*24*time.Hour {
				r.Unjudged = append(r.Unjudged, unusedPrivateCA+"|"+ca.ARN)
				continue
			}
		default:
			r.Unjudged = append(r.Unjudged, unusedPrivateCA+"|"+ca.ARN)
			continue
		}
		out = append(out, unusedPrivateCAFinding(r, o, ca))
	}
	return out
}

func unusedPrivateCAFinding(r *inventory.Region, o Options, ca inventory.PrivateCA) Finding {
	created, changed, notAfter := ymd(ca.Created), ymd(ca.LastStateChange), ymd(ca.NotAfter)
	typeLabel, modeLabel := pcaTypeLabel(ca.Type), pcaModeLabel(ca.UsageMode)
	keep := "# a person's call: deleting stops the bill"
	if ca.Status == "DISABLED" || ca.Status == "PENDING_CERTIFICATE" || ca.Status == "EXPIRED" {
		keep += "; AWS keeps the CA restorable for 30 days, and a restore bills the days in between"
	}
	res := ca.ID
	if ca.CommonName != "" {
		res += " (" + ca.CommonName + ")"
	}
	f := Finding{
		Check: unusedPrivateCA, Region: r.Name, Resource: res, Key: ca.ARN,
		Facts: facts("ca", ca.ARN, "state", ca.Status, "ca_type", ca.Type, "usage_mode", ca.UsageMode,
			"common_name", ca.CommonName, "created", created, "last_change", changed, "not_after", notAfter),
		Evidence: []string{fmt.Sprintf("AWS Private CA ListCertificateAuthorities: %s, %s, %s, created %s, last changed %s, CA certificate valid until %s, CRL %s, OCSP %s",
			ca.Status, ca.Type, ca.UsageMode, orUnknown(created), orUnknown(changed), orUnknown(notAfter), onOff(ca.CRL), onOff(ca.OCSP))},
		Fix: []string{
			keep,
			fmt.Sprintf("# aws acm-pca delete-certificate-authority --region %s --certificate-authority-arn %s --permanent-deletion-time-in-days 30", r.Name, ca.ARN),
		},
	}
	if ca.Status == "DISABLED" {
		f.Fix = append([]string{f.Fix[0], "# certificates it issued stay valid until they expire and can't be revoked after the delete: revoke any still in use first (a disabled CA can still revoke), or for a subordinate CA revoke its CA certificate at the parent, as AWS recommends"}, f.Fix[1:]...)
	}
	detail := fmt.Sprintf("%s CA (%s mode), state %s: it can't issue", typeLabel, modeLabel, ca.Status)
	switch ca.Status {
	case "PENDING_CERTIFICATE":
		f.Confidence = "high"
		f.Detail = detail + "; it never got a CA certificate"
		f.Why = "a CA still waiting for an outside CA to sign its certificate looks the same; that rarely takes 30 days"
	case "EXPIRED":
		f.Confidence = "medium"
		if !ca.NotAfter.IsZero() && o.Now.Sub(ca.NotAfter) >= 30*24*time.Hour {
			f.Confidence = "high"
		}
		f.Detail = detail + fmt.Sprintf("; its CA certificate expired on %s, and everything it issued expired with it", orUnknown(notAfter))
		f.Why = "a CA waiting for its renewed certificate looks the same"
	case "DISABLED":
		f.Confidence = "low"
		f.Detail = detail + fmt.Sprintf("; disabled since %s", orUnknown(changed))
		switch {
		case ca.UsageMode == "SHORT_LIVED_CERTIFICATE" && !ca.LastStateChange.IsZero() && o.Now.Sub(ca.LastStateChange) >= 7*24*time.Hour:
			f.Confidence = "high"
			f.Why = "its certificates last 7 days at most, so none is still valid; a CA disabled on purpose and meant to be turned back on looks the same"
		case ca.Type == "ROOT":
			f.Why = "a root kept disabled on purpose looks the same"
		case ca.Type == "SUBORDINATE":
			f.Why = "a subordinate kept disabled until its certificates expire looks the same; that is how AWS says to retire a CA"
			if !ca.LastStateChange.IsZero() && o.Now.Sub(ca.LastStateChange) >= 395*24*time.Hour && r.PCA.ACMRead && !r.PCA.ACMNamed[ca.ARN] {
				f.Confidence = "medium"
				f.Evidence = append(f.Evidence, "ACM ListCertificates and DescribeCertificate: no certificate in this account names it as its issuer")
			}
		default:
			f.Why = "a Private CA disabled on purpose looks the same"
		}
	}
	if ca.Shared {
		f.Confidence = "low"
		f.Why += "; it's shared with other accounts through its resource policy"
		f.Evidence = append(f.Evidence, "AWS Private CA GetPolicy: a resource policy is attached")
	}
	if r.PCA.Anchored[ca.ARN] {
		f.Confidence = "low"
		f.Why += "; IAM Roles Anywhere trusts it"
		f.Evidence = append(f.Evidence, "IAM Roles Anywhere ListTrustAnchors: a trust anchor names this CA")
	}
	if !ca.PolicyRead || !r.PCA.AnchorsRead {
		f.Confidence = "low"
		f.Why += "; its resource policy or the IAM Roles Anywhere trust anchors couldn't be read, so whether another account or a trust anchor uses it isn't known"
	}
	usd, regional, read := pcaMonthly(r, o, ca.UsageMode)
	if !read {
		f.Detail += "; not priced: the region's Private CA price couldn't be read"
	} else {
		f.MonthlyUSD, f.Priced = usd, true
		f.Detail += fmt.Sprintf("; billed $%.2f a month until it's deleted", usd)
		if !regional {
			f.Detail += " (the us-east-1 price; other regions may differ)"
		}
	}
	return f
}

func pcaTypeLabel(t string) string {
	switch t {
	case "ROOT":
		return "root"
	case "SUBORDINATE":
		return "subordinate"
	default:
		return strings.ToLower(t)
	}
}

func pcaModeLabel(mode string) string {
	if mode == "SHORT_LIVED_CERTIFICATE" {
		return "short-lived"
	}
	return "general-purpose"
}

func ymd(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
