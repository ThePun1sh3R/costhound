package pricing

// Private CA monthly rates in us-east-1 from the AWS Price List
// (AWSCertificateManager, unit CertificateAuthorities), verified 2026-11-22:
// USE1-PaidPrivateCA $400, USE1-ShortLivedCertificatePrivateCA $50.
const (
	PCAGeneralPurposeMonthly = 400.00
	PCAShortLivedMonthly     = 50.00
)

// PCAMonthly returns the built-in CA-month price for a usage mode.
func PCAMonthly(mode string) float64 {
	if mode == "SHORT_LIVED_CERTIFICATE" {
		return PCAShortLivedMonthly
	}
	return PCAGeneralPurposeMonthly
}
