package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeCapacity struct{ fail bool }

func (f fakeCapacity) DescribeCapacityReservations(_ context.Context, in *ec2.DescribeCapacityReservationsInput, _ ...func(*ec2.Options)) (*ec2.DescribeCapacityReservationsOutput, error) {
	if f.fail {
		return nil, errors.New("UnauthorizedOperation")
	}
	cr := func(id, owner, payer string) ec2types.CapacityReservation {
		x := ec2types.CapacityReservation{CapacityReservationId: aws.String(id), InstanceType: aws.String("m5.large"),
			InstancePlatform: ec2types.CapacityReservationInstancePlatformLinuxUnix, AvailabilityZone: aws.String("eu-west-1a"),
			TotalInstanceCount: aws.Int32(4), AvailableInstanceCount: aws.Int32(3), State: ec2types.CapacityReservationStateActive,
			ReservationType: ec2types.CapacityReservationTypeDefault, OwnerId: aws.String(owner),
			StartDate: aws.Time(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)), InstanceMatchCriteria: ec2types.InstanceMatchCriteriaTargeted}
		if payer != "" {
			x.UnusedReservationBillingOwnerId = aws.String(payer)
		}
		return x
	}
	if in.NextToken == nil {
		mine, shared, toUs, away := cr("cr-mine", "111111111111", ""), cr("cr-shared", "222222222222", ""), cr("cr-to-us", "222222222222", "111111111111"), cr("cr-away", "111111111111", "333333333333")
		mine.EndDateType, mine.EndDate = ec2types.EndDateTypeLimited, aws.Time(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
		mine.CommitmentInfo = &ec2types.CapacityReservationCommitmentInfo{CommittedInstanceCount: aws.Int32(2), CommitmentEndDate: aws.Time(time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC))}
		mine.Tenancy, mine.InstanceMatchCriteria = ec2types.CapacityReservationTenancyDedicated, ec2types.InstanceMatchCriteriaOpen
		return &ec2.DescribeCapacityReservationsOutput{NextToken: aws.String("p2"), CapacityReservations: []ec2types.CapacityReservation{mine, shared, toUs, away}}, nil
	}
	fleet, outpost, loan := cr("cr-fleet", "111111111111", ""), cr("cr-outpost", "111111111111", ""), cr("cr-loan", "111111111111", "")
	fleet.CapacityReservationFleetId = aws.String("crf-1")
	outpost.OutpostArn = aws.String("arn:aws:outposts:eu-west-1:111111111111:outpost/op-0123456789abcdef0")
	loan.Interruptible = aws.Bool(true)
	return &ec2.DescribeCapacityReservationsOutput{CapacityReservations: []ec2types.CapacityReservation{fleet, outpost, loan}}, nil
}

func TestLoadCapacityReservations(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}, Capacity: fakeCapacity{}})
	if !r.CapacityListed || len(r.CapacityReservations) != 7 {
		t.Fatalf("both pages: %v %d", r.CapacityListed, len(r.CapacityReservations))
	}
	got := map[string]CapacityReservation{}
	for _, cr := range r.CapacityReservations {
		got[cr.ID] = cr
	}
	// Ours to pay: owned with no other payer, or billing assigned to us.
	for id, want := range map[string]bool{"cr-mine": true, "cr-shared": false, "cr-to-us": true, "cr-away": false, "cr-fleet": true} {
		if got[id].Billed != want {
			t.Errorf("%s: billed %v, want %v", id, got[id].Billed, want)
		}
	}
	m := got["cr-mine"]
	if !m.Open || !m.Dedicated || m.End.IsZero() || m.Committed != 2 || m.CommitEnd.IsZero() || m.Total != 4 || m.Available != 3 || m.Start.IsZero() || m.Elsewhere != "" {
		t.Errorf("mine: %+v", m)
	}
	if got["cr-fleet"].Elsewhere == "" || got["cr-outpost"].Elsewhere == "" || got["cr-loan"].Elsewhere == "" || !got["cr-shared"].End.IsZero() {
		t.Errorf("fleet, Outpost, interruptible: %+v", got)
	}
	r = Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}, Capacity: fakeCapacity{fail: true}})
	if r.CapacityListed || r.CapacityReservations != nil || !strings.Contains(skipsOf(r), "ec2:DescribeCapacityReservations") {
		t.Errorf("refused: not listed, a skip: %v", r.Skipped)
	}
}
