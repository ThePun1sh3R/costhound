package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/acmpca"
	pcatypes "github.com/aws/aws-sdk-go-v2/service/acmpca/types"
	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
	ratypes "github.com/aws/aws-sdk-go-v2/service/rolesanywhere/types"
)

type fakePCA struct {
	list   func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error)
	policy func(*acmpca.GetPolicyInput) (*acmpca.GetPolicyOutput, error)
}

func (f fakePCA) ListCertificateAuthorities(_ context.Context, in *acmpca.ListCertificateAuthoritiesInput, _ ...func(*acmpca.Options)) (*acmpca.ListCertificateAuthoritiesOutput, error) {
	return f.list(in)
}

func (f fakePCA) GetPolicy(_ context.Context, in *acmpca.GetPolicyInput, _ ...func(*acmpca.Options)) (*acmpca.GetPolicyOutput, error) {
	return f.policy(in)
}

type fakeRolesAnywhere struct {
	list func(*rolesanywhere.ListTrustAnchorsInput) (*rolesanywhere.ListTrustAnchorsOutput, error)
}

func (f fakeRolesAnywhere) ListTrustAnchors(_ context.Context, in *rolesanywhere.ListTrustAnchorsInput, _ ...func(*rolesanywhere.Options)) (*rolesanywhere.ListTrustAnchorsOutput, error) {
	return f.list(in)
}

type fakeACM struct {
	list     func(*acm.ListCertificatesInput) (*acm.ListCertificatesOutput, error)
	describe func(*acm.DescribeCertificateInput) (*acm.DescribeCertificateOutput, error)
}

func (f fakeACM) ListCertificates(_ context.Context, in *acm.ListCertificatesInput, _ ...func(*acm.Options)) (*acm.ListCertificatesOutput, error) {
	return f.list(in)
}

func (f fakeACM) DescribeCertificate(_ context.Context, in *acm.DescribeCertificateInput, _ ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error) {
	return f.describe(in)
}

func TestLoadPCAReadsOnlyDecisionFields(t *testing.T) {
	const discard, arn = "GivenName-sentinel", "arn:aws:acm-pca:eu-west-1:1:certificate-authority/ca-1"
	created, changed, notAfter := time.Unix(1700000000, 0), time.Unix(1700100000, 0), time.Unix(1800000000, 0)
	var policyARNs, keyTypes []string
	var describeARNs []string
	c := fakePCA{
		list: func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error) {
			return &acmpca.ListCertificateAuthoritiesOutput{CertificateAuthorities: []pcatypes.CertificateAuthority{{
				Arn: aws.String(arn), Status: pcatypes.CertificateAuthorityStatusDisabled,
				Type: pcatypes.CertificateAuthorityTypeSubordinate, UsageMode: pcatypes.CertificateAuthorityUsageModeGeneralPurpose,
				CreatedAt: &created, LastStateChangeAt: &changed, NotAfter: &notAfter,
				CertificateAuthorityConfiguration: &pcatypes.CertificateAuthorityConfiguration{
					Subject: &pcatypes.ASN1Subject{CommonName: aws.String("Issuing CA"), GivenName: aws.String(discard), Surname: aws.String(discard)},
				},
				RevocationConfiguration: &pcatypes.RevocationConfiguration{
					CrlConfiguration:  &pcatypes.CrlConfiguration{Enabled: aws.Bool(true)},
					OcspConfiguration: &pcatypes.OcspConfiguration{Enabled: aws.Bool(true)},
				},
			}}}, nil
		},
		policy: func(in *acmpca.GetPolicyInput) (*acmpca.GetPolicyOutput, error) {
			policyARNs = append(policyARNs, aws.ToString(in.ResourceArn))
			return &acmpca.GetPolicyOutput{Policy: aws.String(`{"Version":"2012-10-17"}`)}, nil
		},
	}
	ra := fakeRolesAnywhere{list: func(*rolesanywhere.ListTrustAnchorsInput) (*rolesanywhere.ListTrustAnchorsOutput, error) {
		return &rolesanywhere.ListTrustAnchorsOutput{TrustAnchors: []ratypes.TrustAnchorDetail{{
			Source: &ratypes.Source{SourceData: &ratypes.SourceDataMemberAcmPcaArn{Value: arn}},
		}}}, nil
	}}
	acmc := fakeACM{
		list: func(in *acm.ListCertificatesInput) (*acm.ListCertificatesOutput, error) {
			if in.Includes == nil {
				t.Fatal("ListCertificates must pass Includes.KeyTypes")
			}
			for _, k := range in.Includes.KeyTypes {
				keyTypes = append(keyTypes, string(k))
			}
			wantStatuses := []acmtypes.CertificateStatus{
				acmtypes.CertificateStatusIssued, acmtypes.CertificateStatusInactive, acmtypes.CertificateStatusPendingValidation,
			}
			if !slices.Equal(in.CertificateStatuses, wantStatuses) {
				t.Fatalf("statuses: %v", in.CertificateStatuses)
			}
			return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{
				{CertificateArn: aws.String("cert-private"), Type: acmtypes.CertificateTypePrivate},
				{CertificateArn: aws.String("cert-amazon"), Type: acmtypes.CertificateTypeAmazonIssued},
			}}, nil
		},
		describe: func(in *acm.DescribeCertificateInput) (*acm.DescribeCertificateOutput, error) {
			describeARNs = append(describeARNs, aws.ToString(in.CertificateArn))
			return &acm.DescribeCertificateOutput{Certificate: &acmtypes.CertificateDetail{
				CertificateAuthorityArn: aws.String(arn),
				DomainName:              aws.String("secret.example.com"),
			}}, nil
		},
	}
	r := &Region{}
	loadPCA(t.Context(), r, c, ra, acmc, func(op string, _ error) { t.Errorf("unexpected skip %s", op) })
	if r.PCA == nil || len(r.PCA.CAs) != 1 {
		t.Fatalf("wanted one CA: %+v", r.PCA)
	}
	ca := r.PCA.CAs[0]
	if ca.ID != "ca-1" || ca.CommonName != "Issuing CA" || !ca.CRL || !ca.OCSP || !ca.Shared || !ca.PolicyRead ||
		!ca.Created.Equal(created) || !r.PCA.AnchorsRead || !r.PCA.Anchored[arn] || !r.PCA.ACMRead || !r.PCA.ACMNamed[arn] {
		t.Fatalf("incomplete PCA inventory: %+v anchors=%v acm=%v", ca, r.PCA.Anchored, r.PCA.ACMNamed)
	}
	if !slices.Equal(policyARNs, []string{arn}) || !slices.Equal(describeARNs, []string{"cert-private"}) {
		t.Fatalf("policy/describe targets: %v %v", policyARNs, describeARNs)
	}
	for _, k := range acmtypes.KeyAlgorithm("").Values() {
		if !slices.Contains(keyTypes, string(k)) {
			t.Errorf("missing key type %s in %v", k, keyTypes)
		}
	}
	encoded, err := json.Marshal(r.PCA)
	if err != nil || strings.Contains(string(encoded), discard) || strings.Contains(string(encoded), "secret.example.com") {
		t.Fatal("planted subject fields or ACM domain survived loading")
	}
}

func TestLoadPCAGetPolicyAndFailures(t *testing.T) {
	arnPending := "arn:aws:acm-pca:eu-west-1:1:certificate-authority/pending"
	arnActive := "arn:aws:acm-pca:eu-west-1:1:certificate-authority/active"
	arnExpired := "arn:aws:acm-pca:eu-west-1:1:certificate-authority/expired"
	var policyARNs []string
	c := fakePCA{
		list: func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error) {
			return &acmpca.ListCertificateAuthoritiesOutput{CertificateAuthorities: []pcatypes.CertificateAuthority{
				{Arn: aws.String(arnPending), Status: pcatypes.CertificateAuthorityStatusPendingCertificate, Type: pcatypes.CertificateAuthorityTypeRoot},
				{Arn: aws.String(arnActive), Status: pcatypes.CertificateAuthorityStatusActive, Type: pcatypes.CertificateAuthorityTypeRoot},
				{Arn: aws.String(arnExpired), Status: pcatypes.CertificateAuthorityStatusExpired, Type: pcatypes.CertificateAuthorityTypeRoot},
			}}, nil
		},
		policy: func(in *acmpca.GetPolicyInput) (*acmpca.GetPolicyOutput, error) {
			arn := aws.ToString(in.ResourceArn)
			policyARNs = append(policyARNs, arn)
			switch arn {
			case arnPending:
				return nil, &pcatypes.ResourceNotFoundException{Message: aws.String("none")}
			case arnExpired:
				return nil, errors.New("denied")
			default:
				t.Fatalf("GetPolicy called for %s", arn)
				return nil, nil
			}
		},
	}
	r := &Region{}
	var skips []string
	loadPCA(t.Context(), r, c, fakeRolesAnywhere{list: func(*rolesanywhere.ListTrustAnchorsInput) (*rolesanywhere.ListTrustAnchorsOutput, error) {
		return nil, errors.New("no rolesanywhere")
	}}, nil, func(op string, _ error) { skips = append(skips, op) })
	byARN := map[string]PrivateCA{}
	for _, ca := range r.PCA.CAs {
		byARN[ca.ARN] = ca
	}
	if !slices.Equal(policyARNs, []string{arnPending, arnExpired}) {
		t.Fatalf("GetPolicy targets: %v", policyARNs)
	}
	if !byARN[arnPending].PolicyRead || byARN[arnPending].Shared || byARN[arnExpired].PolicyRead || byARN[arnActive].PolicyRead {
		t.Fatalf("policy flags: %+v", byARN)
	}
	if r.PCA.AnchorsRead || !slices.Contains(skips, "rolesanywhere:ListTrustAnchors") || !slices.Contains(skips, "acm-pca:GetPolicy") {
		t.Fatalf("expected unread anchors and a policy skip: anchors=%v skips=%v", r.PCA.AnchorsRead, skips)
	}
}

func TestLoadPCAFailedListLeavesNil(t *testing.T) {
	c := fakePCA{list: func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error) {
		return nil, errors.New("denied")
	}}
	r := &Region{}
	var skips []string
	loadPCA(t.Context(), r, c, nil, nil, func(op string, _ error) { skips = append(skips, op) })
	if r.PCA != nil || !slices.Equal(skips, []string{"acm-pca:ListCertificateAuthorities"}) {
		t.Fatalf("failed list must leave PCA nil: %+v skips=%v", r.PCA, skips)
	}
}

func TestLoadPCAEmptyUsageModeIsGeneralPurpose(t *testing.T) {
	c := fakePCA{
		list: func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error) {
			return &acmpca.ListCertificateAuthoritiesOutput{CertificateAuthorities: []pcatypes.CertificateAuthority{{
				Arn: aws.String("arn:aws:acm-pca:eu-west-1:1:certificate-authority/x"), Status: pcatypes.CertificateAuthorityStatusActive,
			}}}, nil
		},
		policy: func(*acmpca.GetPolicyInput) (*acmpca.GetPolicyOutput, error) {
			t.Fatal("ACTIVE CAs must not call GetPolicy")
			return nil, nil
		},
	}
	r := &Region{}
	loadPCA(t.Context(), r, c, nil, nil, func(op string, _ error) { t.Errorf("unexpected skip %s", op) })
	if r.PCA.CAs[0].UsageMode != "GENERAL_PURPOSE" || r.PCA.AnchorsRead || r.PCA.ACMRead {
		t.Fatalf("empty usage mode / no candidate reads: %+v", r.PCA)
	}
}

// ACM costs one DescribeCertificate per certificate and its limits are low:
// no general-purpose DISABLED subordinate, no ACM call at all; and it stops
// once every candidate is named. An empty policy isn't a share.
func TestLoadPCAACMOnlyWhenNeeded(t *testing.T) {
	old := time.Unix(1600000000, 0)
	ca := func(id string, status pcatypes.CertificateAuthorityStatus, typ pcatypes.CertificateAuthorityType, mode pcatypes.CertificateAuthorityUsageMode) pcatypes.CertificateAuthority {
		return pcatypes.CertificateAuthority{Arn: aws.String("arn:aws:acm-pca:eu-west-1:1:certificate-authority/" + id), Status: status, Type: typ,
			UsageMode: mode, CreatedAt: &old, LastStateChangeAt: &old}
	}
	list := func(cas ...pcatypes.CertificateAuthority) fakePCA {
		return fakePCA{
			list: func(*acmpca.ListCertificateAuthoritiesInput) (*acmpca.ListCertificateAuthoritiesOutput, error) {
				return &acmpca.ListCertificateAuthoritiesOutput{CertificateAuthorities: cas}, nil
			},
			policy: func(*acmpca.GetPolicyInput) (*acmpca.GetPolicyOutput, error) {
				return &acmpca.GetPolicyOutput{Policy: aws.String("  ")}, nil // empty: not shared
			},
		}
	}
	ra := fakeRolesAnywhere{list: func(*rolesanywhere.ListTrustAnchorsInput) (*rolesanywhere.ListTrustAnchorsOutput, error) {
		return &rolesanywhere.ListTrustAnchorsOutput{}, nil
	}}
	var listed, described int
	acmc := fakeACM{
		list: func(*acm.ListCertificatesInput) (*acm.ListCertificatesOutput, error) {
			listed++
			return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{
				{CertificateArn: aws.String("cert-1"), Type: acmtypes.CertificateTypePrivate},
				{CertificateArn: aws.String("cert-2"), Type: acmtypes.CertificateTypePrivate},
			}}, nil
		},
		describe: func(*acm.DescribeCertificateInput) (*acm.DescribeCertificateOutput, error) {
			described++
			return &acm.DescribeCertificateOutput{Certificate: &acmtypes.CertificateDetail{
				CertificateAuthorityArn: aws.String("arn:aws:acm-pca:eu-west-1:1:certificate-authority/sub")}}, nil
		},
	}
	noSkip := func(op string, _ error) { t.Errorf("unexpected skip %s", op) }

	// A disabled root, an expired CA and a disabled short-lived subordinate: no ACM call.
	r := &Region{}
	loadPCA(t.Context(), r, list(ca("root", pcatypes.CertificateAuthorityStatusDisabled, pcatypes.CertificateAuthorityTypeRoot, pcatypes.CertificateAuthorityUsageModeGeneralPurpose),
		ca("exp", pcatypes.CertificateAuthorityStatusExpired, pcatypes.CertificateAuthorityTypeSubordinate, pcatypes.CertificateAuthorityUsageModeGeneralPurpose),
		ca("short", pcatypes.CertificateAuthorityStatusDisabled, pcatypes.CertificateAuthorityTypeSubordinate, pcatypes.CertificateAuthorityUsageModeShortLivedCertificate)),
		ra, acmc, noSkip)
	if listed != 0 || described != 0 || r.PCA.ACMRead {
		t.Errorf("no general-purpose disabled subordinate: no ACM call, got %d list %d describe", listed, described)
	}
	for _, c := range r.PCA.CAs {
		if c.Shared || !c.PolicyRead {
			t.Errorf("an empty policy is read and isn't a share: %+v", c)
		}
	}

	// One candidate, named by the first certificate: one DescribeCertificate, not two.
	r = &Region{}
	loadPCA(t.Context(), r, list(ca("sub", pcatypes.CertificateAuthorityStatusDisabled, pcatypes.CertificateAuthorityTypeSubordinate, pcatypes.CertificateAuthorityUsageModeGeneralPurpose)),
		ra, acmc, noSkip)
	if listed != 1 || described != 1 || !r.PCA.ACMRead || !r.PCA.ACMNamed["arn:aws:acm-pca:eu-west-1:1:certificate-authority/sub"] {
		t.Errorf("stop once every candidate is named: %d list %d describe, %+v", listed, described, r.PCA)
	}

	// A throttled describe leaves ACM unread (the finding then stays, low).
	throttled := acmc
	throttled.describe = func(*acm.DescribeCertificateInput) (*acm.DescribeCertificateOutput, error) {
		return nil, errors.New("ThrottlingException: Rate exceeded")
	}
	r = &Region{}
	var skips []string
	loadPCA(t.Context(), r, list(ca("sub", pcatypes.CertificateAuthorityStatusDisabled, pcatypes.CertificateAuthorityTypeSubordinate, pcatypes.CertificateAuthorityUsageModeGeneralPurpose)),
		ra, throttled, func(op string, _ error) { skips = append(skips, op) })
	if r.PCA.ACMRead || !slices.Equal(skips, []string{"acm:DescribeCertificate"}) {
		t.Errorf("a throttled read is unread, one skip: %v %+v", skips, r.PCA)
	}
}
