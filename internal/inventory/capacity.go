package inventory

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// CapacityReservation is what unused-capacity-reservation keeps of an EC2
// On-Demand Capacity Reservation.
type CapacityReservation struct {
	ID, Type, Platform, AZ string
	Dedicated              bool // tenancy dedicated
	Total, Available       int
	Open                   bool      // InstanceMatchCriteria open: a matching instance uses it without asking
	State, Kind            string    // Kind: ReservationType, default or capacity-block
	Start, End             time.Time // End zero: unlimited
	CommitEnd              time.Time // a future-dated reservation's commitment ends: until then it can't go below Committed
	Committed              int
	Billed                 bool // this account pays for its unused capacity
	Owned                  bool // and owns it: only the owner can cut or cancel it
	Owner                  string
	Elsewhere              string // why another part of AWS owns it: a fleet, an Outpost, interruptible ("" = none)
}

// loadCapacityReservations reads the region's Capacity Reservations,
// including those shared with the account. account is the scanned account:
// only reservations whose unused capacity it pays for are Billed.
func loadCapacityReservations(ctx context.Context, r *Region, c ec2.DescribeCapacityReservationsAPIClient, account string, skip func(string, error)) {
	var out []CapacityReservation
	p := ec2.NewDescribeCapacityReservationsPaginator(c, &ec2.DescribeCapacityReservationsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeCapacityReservationsOutput) {
		for _, x := range o.CapacityReservations {
			cr := CapacityReservation{ID: aws.ToString(x.CapacityReservationId), Type: aws.ToString(x.InstanceType), Platform: string(x.InstancePlatform),
				AZ: aws.ToString(x.AvailabilityZone), Dedicated: x.Tenancy == ec2types.CapacityReservationTenancyDedicated,
				Total: int(aws.ToInt32(x.TotalInstanceCount)), Available: int(aws.ToInt32(x.AvailableInstanceCount)),
				Open: x.InstanceMatchCriteria == ec2types.InstanceMatchCriteriaOpen, State: string(x.State), Kind: string(x.ReservationType)}
			if x.StartDate != nil {
				cr.Start = *x.StartDate
			}
			if x.EndDateType == ec2types.EndDateTypeLimited && x.EndDate != nil {
				cr.End = *x.EndDate
			}
			if ci := x.CommitmentInfo; ci != nil && ci.CommitmentEndDate != nil {
				cr.CommitEnd, cr.Committed = *ci.CommitmentEndDate, int(aws.ToInt32(ci.CommittedInstanceCount))
			}
			owner, payer := aws.ToString(x.OwnerId), aws.ToString(x.UnusedReservationBillingOwnerId)
			cr.Owner, cr.Owned = owner, account != "" && owner == account
			cr.Billed = account != "" && (owner == account && (payer == "" || payer == account) || payer == account)
			switch {
			case aws.ToString(x.CapacityReservationFleetId) != "":
				cr.Elsewhere = "a Capacity Reservation Fleet manages it"
			case aws.ToString(x.OutpostArn) != "":
				cr.Elsewhere = "it's on an Outpost, paid for with the Outpost"
			case aws.ToBool(x.Interruptible):
				cr.Elsewhere = "it's interruptible capacity lent by another reservation"
			}
			out = append(out, cr)
		}
	}); err != nil {
		skip("ec2:DescribeCapacityReservations", err)
		return
	}
	r.CapacityReservations, r.CapacityListed = out, true
}
