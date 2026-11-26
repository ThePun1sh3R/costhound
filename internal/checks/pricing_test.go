package checks

import "testing"

func TestPricingOf(t *testing.T) {
	if PricingOf("unattached-ebs") != PriceLiveList {
		t.Fatal(PricingOf("unattached-ebs"))
	}
	if PricingOf("s3-standard-no-tiering") != PriceCostOnly {
		t.Fatal(PricingOf("s3-standard-no-tiering"))
	}
	if PricingOf("old-instance-type") != PriceUnpriced {
		t.Fatal(PricingOf("old-instance-type"))
	}
}
