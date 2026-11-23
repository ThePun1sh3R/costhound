package inventory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/acmpca"
	pcatypes "github.com/aws/aws-sdk-go-v2/service/acmpca/types"
	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
	ratypes "github.com/aws/aws-sdk-go-v2/service/rolesanywhere/types"
)

// PrivateCA is what costhound keeps of an ACM Private CA: identifiers, billing
// state, and the flags that lower confidence when the CA can't issue.
type PrivateCA struct {
	ARN, ID, Status, Type, UsageMode, CommonName string
	Created, LastStateChange, NotAfter           time.Time
	CRL, OCSP                                    bool
	Shared                                       bool
	PolicyRead                                   bool
}

// PCA is a region's Private CAs and the trust / ACM reads that qualify them.
// A nil PCA on the region means the list failed or was never called.
type PCA struct {
	CAs         []PrivateCA
	AnchorsRead bool
	Anchored    map[string]bool // CA ARN -> IAM Roles Anywhere trusts it
	ACMRead     bool
	ACMNamed    map[string]bool // CA ARN -> ACM has issued a PRIVATE certificate from it
}

// PCAAPI is the ACM Private CA inventory used by unused-private-ca.
type PCAAPI interface {
	acmpca.ListCertificateAuthoritiesAPIClient
	GetPolicy(context.Context, *acmpca.GetPolicyInput, ...func(*acmpca.Options)) (*acmpca.GetPolicyOutput, error)
}

// RolesAnywhereAPI lists IAM Roles Anywhere trust anchors.
type RolesAnywhereAPI interface {
	rolesanywhere.ListTrustAnchorsAPIClient
}

// ACMAPI lists ACM certificates and describes PRIVATE ones for their CA ARN.
type ACMAPI interface {
	acm.ListCertificatesAPIClient
	DescribeCertificate(context.Context, *acm.DescribeCertificateInput, ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
}

func loadPCA(ctx context.Context, r *Region, c PCAAPI, ra RolesAnywhereAPI, acmc ACMAPI, skip func(string, error)) {
	const listOp = "acm-pca:ListCertificateAuthorities"
	var cas []PrivateCA
	p := acmpca.NewListCertificateAuthoritiesPaginator(c, &acmpca.ListCertificateAuthoritiesInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *acmpca.ListCertificateAuthoritiesOutput) {
		for _, x := range o.CertificateAuthorities {
			arn := aws.ToString(x.Arn)
			if arn == "" {
				skip(listOp, errors.New("a listed Private CA is missing its ARN"))
				continue
			}
			_, id, _ := strings.Cut(arn, "certificate-authority/")
			mode := string(x.UsageMode)
			if mode == "" {
				mode = "GENERAL_PURPOSE"
			}
			ca := PrivateCA{
				ARN: arn, ID: id, Status: string(x.Status), Type: string(x.Type), UsageMode: mode,
				Created: aws.ToTime(x.CreatedAt), LastStateChange: aws.ToTime(x.LastStateChangeAt), NotAfter: aws.ToTime(x.NotAfter),
			}
			if x.CertificateAuthorityConfiguration != nil && x.CertificateAuthorityConfiguration.Subject != nil {
				ca.CommonName = aws.ToString(x.CertificateAuthorityConfiguration.Subject.CommonName)
			}
			if x.RevocationConfiguration != nil {
				if x.RevocationConfiguration.CrlConfiguration != nil {
					ca.CRL = aws.ToBool(x.RevocationConfiguration.CrlConfiguration.Enabled)
				}
				if x.RevocationConfiguration.OcspConfiguration != nil {
					ca.OCSP = aws.ToBool(x.RevocationConfiguration.OcspConfiguration.Enabled)
				}
			}
			cas = append(cas, ca)
		}
	}); err != nil {
		skip(listOp, err)
		return
	}

	pca := &PCA{CAs: cas, Anchored: map[string]bool{}, ACMNamed: map[string]bool{}}
	r.PCA = pca

	candidates := false
	named := map[string]bool{} // general-purpose DISABLED subordinates: the ones ACM may name
	for i := range pca.CAs {
		ca := &pca.CAs[i]
		switch ca.Status {
		case "PENDING_CERTIFICATE", "EXPIRED", "DISABLED":
			candidates = true
			loadPCAPolicy(ctx, c, ca, skip)
		}
		if ca.Status == "DISABLED" && ca.Type == "SUBORDINATE" && ca.UsageMode != "SHORT_LIVED_CERTIFICATE" {
			named[ca.ARN] = false // ACM can't issue from a short-lived CA
		}
	}
	if candidates {
		loadPCATrustAnchors(ctx, pca, ra, skip)
	}
	if len(named) > 0 {
		loadPCAACM(ctx, pca, acmc, named, skip)
	}
}

func loadPCAPolicy(ctx context.Context, c PCAAPI, ca *PrivateCA, skip func(string, error)) {
	const op = "acm-pca:GetPolicy"
	out, err := c.GetPolicy(ctx, &acmpca.GetPolicyInput{ResourceArn: aws.String(ca.ARN)})
	var notFound *pcatypes.ResourceNotFoundException
	switch {
	case err == nil:
		ca.PolicyRead = true
		ca.Shared = out != nil && strings.TrimSpace(aws.ToString(out.Policy)) != ""
	case errors.As(err, &notFound):
		ca.PolicyRead = true
	default:
		skip(op, err)
	}
}

func loadPCATrustAnchors(ctx context.Context, pca *PCA, ra RolesAnywhereAPI, skip func(string, error)) {
	const op = "rolesanywhere:ListTrustAnchors"
	if ra == nil {
		return
	}
	p := rolesanywhere.NewListTrustAnchorsPaginator(ra, &rolesanywhere.ListTrustAnchorsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *rolesanywhere.ListTrustAnchorsOutput) {
		for _, x := range o.TrustAnchors {
			if x.Source == nil || x.Source.SourceData == nil {
				continue
			}
			if m, ok := x.Source.SourceData.(*ratypes.SourceDataMemberAcmPcaArn); ok && m.Value != "" {
				pca.Anchored[m.Value] = true
			}
		}
	}); err != nil {
		skip(op, err)
		return
	}
	pca.AnchorsRead = true
}

// loadPCAACM reads which CAs ACM's live PRIVATE certificates name: one
// DescribeCertificate per certificate (ACM's limits are low), so only when a
// candidate exists, and it stops once every candidate in want is named. A
// failed or throttled read leaves ACMRead false: the finding stays, low.
func loadPCAACM(ctx context.Context, pca *PCA, acmc ACMAPI, want map[string]bool, skip func(string, error)) {
	const listOp, describeOp = "acm:ListCertificates", "acm:DescribeCertificate"
	if acmc == nil {
		return
	}
	var arns []string
	p := acm.NewListCertificatesPaginator(acmc, &acm.ListCertificatesInput{
		CertificateStatuses: []acmtypes.CertificateStatus{
			acmtypes.CertificateStatusIssued,
			acmtypes.CertificateStatusInactive,
			acmtypes.CertificateStatusPendingValidation,
		},
		Includes: &acmtypes.Filters{KeyTypes: acmtypes.KeyAlgorithm("").Values()},
	})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *acm.ListCertificatesOutput) {
		for _, s := range o.CertificateSummaryList {
			if s.Type != acmtypes.CertificateTypePrivate {
				continue
			}
			arn := aws.ToString(s.CertificateArn)
			if arn == "" {
				skip(listOp, errors.New("a listed PRIVATE certificate is missing its ARN"))
				continue
			}
			arns = append(arns, arn)
		}
	}); err != nil {
		skip(listOp, err)
		return
	}
	for _, arn := range arns {
		o, err := acmc.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: aws.String(arn)})
		if err != nil {
			skip(describeOp, err)
			return
		}
		if o.Certificate == nil {
			skip(describeOp, errors.New("the certificate description is missing"))
			return
		}
		if ca := aws.ToString(o.Certificate.CertificateAuthorityArn); ca != "" {
			pca.ACMNamed[ca] = true
			if _, ok := want[ca]; ok && !want[ca] {
				want[ca] = true
				left := 0
				for _, done := range want {
					if !done {
						left++
					}
				}
				if left == 0 {
					break // every candidate is named: the rest can't change a finding
				}
			}
		}
	}
	pca.ACMRead = true
}
