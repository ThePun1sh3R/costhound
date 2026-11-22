package checks

import (
	"fmt"
	"strconv"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// EmptyHostedZones finds public hosted zones with only their NS and SOA
// records: they answer no query, but Route 53 bills each zone, $0.50 a
// month for the first 25 in an account and $0.10 after ("HostedZone",
// AmazonRoute53 offer), so the empty ones are priced from the top. Left
// out: private zones (an empty one answers NXDOMAIN inside its VPCs, which
// can be the point), zones another AWS service made (LinkedService), and
// DNSSEC-signed or unread ones. A zone that exists but isn't empty now is
// unjudged, so an earlier finding is parked, not fixed (one TXT record
// doesn't lower the bill).
func EmptyHostedZones(r *inventory.Region, o Options) []Finding {
	if !r.ZonesListed {
		return nil
	}
	var empty []inventory.HostedZone
	for _, z := range r.Zones {
		switch {
		case z.Private || z.Linked != "":
		case z.Records == 2 && z.DNSSEC == "none":
			empty = append(empty, z)
		default:
			r.Unjudged = append(r.Unjudged, "empty-hosted-zone|"+z.ID)
		}
	}
	n := len(r.Zones)
	var out []Finding
	for _, z := range empty {
		out = append(out, Finding{
			Check: "empty-hosted-zone", Region: "global", Resource: z.ID + " (" + z.Name + ")", Key: z.ID, Confidence: "medium",
			Why:      "a registrar or a parent zone may still delegate the domain to it (that delegation goes first), and a zone made for records still to come looks the same",
			Evidence: []string{fmt.Sprintf("Route 53 ListHostedZones: %d record sets (the NS and SOA records), public, no service owns it; GetDNSSEC: not signing; %d zones in the account", z.Records, n)},
			Fix: []string{
				"# 1. is the domain delegated here? dig NS " + z.Name + " and look at the registrar's or the parent zone's NS records",
				"# 2. remove that delegation first and wait out its TTL: a delegated zone that's deleted can be taken over by someone else",
				"# 3. aws route53 delete-hosted-zone --id " + z.ID,
			},
			Facts: facts("zone_id", z.ID, "zone_name", z.Name, "zones_in_account", itoa(n)),
		})
	}
	PriceEmptyZones(out)
	return out
}

// PriceEmptyZones prices each empty zone at what deleting it alone saves:
// $0.50 while the account has 25 zones or fewer, $0.10 past that (deleting
// more than N-25 of them saves $0.50 each beyond that, which the Detail
// says: the low side, like AlarmFreeTier). An Advanced hosted zone bills
// $5.00 instead, and nothing in the API says which kind a zone is.
func PriceEmptyZones(fs []Finding) {
	var idx []int
	for i := range fs {
		if fs[i].Check == "empty-hosted-zone" {
			idx = append(idx, i)
		}
	}
	for _, i := range idx {
		f := &fs[i]
		n, _ := strconv.Atoi(f.Facts["zones_in_account"])
		usd, beyond := 0.50, ""
		if n > 25 {
			usd = 0.10
			if len(idx) > n-25 {
				beyond = fmt.Sprintf("; deleting more than %d of the empty ones saves $0.50 each beyond that", n-25)
			}
		}
		f.MonthlyUSD, f.Priced = usd, true
		f.Detail = fmt.Sprintf("public hosted zone with only its NS and SOA records: it answers no query, and deleting it saves $%.2f a month (an account's first 25 zones are $0.50 each and the rest $0.10: %d zones here, so $0.10 if your Organization has more than 25%s; an Advanced hosted zone, which the API doesn't show, is $5.00). A zone deleted within 12 hours of being made isn't billed: one made today may be waiting for its records", usd, n, beyond)
	}
}
