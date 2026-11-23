package checks

import (
	"fmt"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// UnusedCapacityReservations finds EC2 On-Demand Capacity Reservations
// holding capacity nobody uses: AWS bills the On-Demand rate for every
// instance a reservation holds, used or not. The free plan judges what's
// available now, a day after it started; paid plans the busiest
// UsedInstanceCount over the usage window, so capacity never used once.
// Only reservations whose unused capacity this account pays for count
// (owner or billing assignment); capacity blocks (prepaid), fleets,
// Outposts and interruptible capacity are left out. A future-dated
// reservation can't go below its commitment before it ends, and one with
// an end date is priced for the hours it has left. A reservation that
// exists without a finding is unjudged, so an earlier finding is parked.
func UnusedCapacityReservations(r *inventory.Region, o Options) []Finding {
	if !r.CapacityListed {
		return nil
	}
	var out []Finding
	for _, cr := range r.CapacityReservations {
		switch cr.State {
		case "cancelled", "failed", "unsupported", "payment-failed":
			continue // gone: the fix, when it had a finding ("cancelling" still bills its wind-down, "expired" ended by itself: parked)
		}
		if f, ok := unusedCR(r, o, cr); ok {
			out = append(out, f)
		} else {
			r.Unjudged = append(r.Unjudged, "unused-capacity-reservation|"+cr.ID)
		}
	}
	return out
}

func unusedCR(r *inventory.Region, o Options, cr inventory.CapacityReservation) (Finding, bool) {
	if cr.State != "active" || !cr.Billed || cr.Elsewhere != "" || cr.Kind == "capacity-block" || cr.Start.IsZero() {
		return Finding{}, false
	}
	age, window := o.Now.Sub(cr.Start), max(o.UsageDays, 1)
	unused, proof, young := crUsage(r, cr, age, window)
	if young {
		return Finding{}, false // read, but younger than the window: parked (the snapshot is for failed reads)
	}
	if unused <= 0 || !proof && age < 24*time.Hour {
		return Finding{}, false
	}
	units, committed := unused, !cr.CommitEnd.IsZero() && cr.CommitEnd.After(o.Now)
	if committed {
		units = min(unused, max(0, cr.Total-cr.Committed))
	}
	hours := pricing.HoursPerMonth
	if !cr.End.IsZero() {
		if hours = min(hours, cr.End.Sub(o.Now).Hours()); hours <= 0 {
			return Finding{}, false
		}
	}
	hour, exact := crHour(r, cr)
	if !exact {
		if ty, ok := pricing.EC2(cr.Type); ok {
			hour = ty.Hour
		}
	}
	match, tenancy, ends := "targeted", "default", "never"
	if cr.Open {
		match = "open"
	}
	if cr.Dedicated {
		tenancy = "dedicated"
	}
	if !cr.End.IsZero() {
		ends = cr.End.UTC().Format("2006-01-02")
	}
	use := fmt.Sprintf("%d of its %d instances unused now", unused, cr.Total)
	if proof {
		use = fmt.Sprintf("%d of its %d instances never used once in the last %d days", unused, cr.Total, window)
	}
	f := Finding{
		Check: "unused-capacity-reservation", Region: r.Name, Resource: cr.ID + " (" + cr.Type + ", " + cr.AZ + ")", Key: cr.ID,
		Detail: fmt.Sprintf("Capacity Reservation for %s %s instances (%s): %s. AWS bills the On-Demand rate for reserved capacity, used or not", cr.Type, cr.Platform, match, use),
		Why: "a reservation kept for disaster recovery or a launch to come looks the same, and capacity released in a busy zone may not come back; " +
			"the saving assumes no discount applies (a regional Reserved Instance or a Savings Plan with spare commitment discounts unused reserved capacity too, so cancelling may save less or nothing)",
		Evidence: []string{fmt.Sprintf("EC2 DescribeCapacityReservations: active since %s, %d reserved, %d available now, %s, this account pays for its unused capacity", cr.Start.UTC().Format("2006-01-02"), cr.Total, cr.Available, match)},
		Facts: facts("capacity_reservation", cr.ID, "instance_type", cr.Type, "platform", cr.Platform, "tenancy", tenancy, "az", cr.AZ,
			"total", itoa(cr.Total), "unused", itoa(unused), "match", match, "ends", ends),
	}
	if proof {
		f.Evidence = append(f.Evidence, fmt.Sprintf("CloudWatch AWS/EC2CapacityReservations UsedInstanceCount over %d days: at most %d", window, cr.Total-unused))
		f.Facts["window_days"] = itoa(window)
	}
	switch { // medium at most: an open reservation unused for weeks is exactly what one kept for disaster recovery looks like
	case proof || !cr.Open:
		f.Confidence = "medium"
	default:
		f.Confidence = "low"
		f.Why = "it's open: any matching instance uses it, and one may just be stopped right now; " + f.Why
	}
	if regionalRI(r, cr.Type, o.Now) {
		f.Confidence = "low"
		f.Why = "an active regional Reserved Instance of its family is in this region, and its discount reaches unused reserved capacity; " + f.Why
	}
	switch {
	case !cr.Owned:
		f.Detail += "; not priced: account " + cr.Owner + " owns it and bills its unused capacity here, and only the owner can cut or cancel it"
	case units == 0:
		f.Detail += fmt.Sprintf("; not priced: it can't go below its commitment of %d before %s", cr.Committed, cr.CommitEnd.UTC().Format("2006-01-02"))
	case hour == 0:
		f.Detail += "; its hourly price wasn't found"
	default:
		f.MonthlyUSD, f.Priced = float64(units)*hour*hours, true
		f.Detail += fmt.Sprintf(": $%.2f a month for %d of them at $%.4f an hour", f.MonthlyUSD, units, hour)
		if !exact {
			f.Detail += " (at least: the us-east-1 shared Linux rate; this region, platform or tenancy may cost more)"
		}
		if hours < pricing.HoursPerMonth {
			f.Detail += fmt.Sprintf(", counting only the %.0f hours it has left", hours)
		}
	}
	if committed && units > 0 && units < unused {
		f.Detail += fmt.Sprintf("; it can't go below %d instances before %s (its commitment)", cr.Committed, cr.CommitEnd.UTC().Format("2006-01-02"))
	}
	if !cr.End.IsZero() {
		f.Detail += "; it ends on " + ends
	}
	keep := cr.Total - units
	f.Fix = []string{"# AWS bills every instance a reservation holds, and capacity released in a busy zone may not come back: a person decides"}
	if !cr.Owned {
		f.Fix = append(f.Fix, "# account "+cr.Owner+" owns it: ask them to cut or cancel it (this account can't)")
		return f, true
	}
	if units > 0 && keep > 0 {
		f.Fix = append(f.Fix, fmt.Sprintf("# aws ec2 modify-capacity-reservation --region %s --capacity-reservation-id %s --instance-count %d", r.Name, cr.ID, keep))
	}
	if !committed {
		f.Fix = append(f.Fix, fmt.Sprintf("# or, if nothing needs it: aws ec2 cancel-capacity-reservation --region %s --capacity-reservation-id %s", r.Name, cr.ID))
	}
	return f, true
}
