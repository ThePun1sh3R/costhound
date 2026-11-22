package inventory

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func linuxX86(id, typ, az string) ec2types.Instance {
	return ec2types.Instance{InstanceId: aws.String(id), InstanceType: ec2types.InstanceType(typ),
		State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, LaunchTime: aws.Time(time.Now().AddDate(0, -3, 0)),
		Architecture: ec2types.ArchitectureValuesX8664, PlatformDetails: aws.String("Linux/UNIX"), UsageOperation: aws.String("RunInstances"),
		Placement: &ec2types.Placement{AvailabilityZone: aws.String(az), Tenancy: ec2types.TenancyDefault}}
}

func TestGravitonCandidate(t *testing.T) {
	if !GravitonCandidate(linuxX86("i-1", "m5.large", "eu-west-1a"), "eu-west-1") {
		t.Fatal("a plain Linux m5.large")
	}
	asg := linuxX86("i-2", "m5.large", "eu-west-1a")
	asg.Tags = []ec2types.Tag{{Key: aws.String("aws:autoscaling:groupName"), Value: aws.String("web")}}
	if !GravitonCandidate(asg, "eu-west-1") {
		t.Error("an Auto Scaling member stays (reported with its group)")
	}
	for name, change := range map[string]func(*ec2types.Instance){
		"SUSE":           func(in *ec2types.Instance) { in.UsageOperation = aws.String("RunInstances:000g") },
		"Windows":        func(in *ec2types.Instance) { in.PlatformDetails = aws.String("Windows") },
		"Wavelength":     func(in *ec2types.Instance) { in.Placement.AvailabilityZone = aws.String("eu-west-1-wl1-lon-wlz-1") },
		"another region": func(in *ec2types.Instance) { in.Placement.AvailabilityZone = aws.String("eu-west-2a") },
		"an Outpost":     func(in *ec2types.Instance) { in.OutpostArn = aws.String("arn:aws:outposts:eu-west-1:1:outpost/op-1") },
		"a host":         func(in *ec2types.Instance) { in.Placement.Tenancy = ec2types.TenancyHost },
		"Beanstalk": func(in *ec2types.Instance) {
			in.Tags = []ec2types.Tag{{Key: aws.String("elasticbeanstalk:environment-name"), Value: aws.String("e")}}
		},
		"Batch": func(in *ec2types.Instance) {
			in.Tags = []ec2types.Tag{{Key: aws.String("aws:batch:compute-environment"), Value: aws.String("c")}}
		},
		"no launch time": func(in *ec2types.Instance) { in.LaunchTime = nil },
	} {
		in := linuxX86("i-3", "m5.large", "eu-west-1a")
		change(&in)
		if GravitonCandidate(in, "eu-west-1") {
			t.Errorf("%s: not a candidate", name)
		}
	}
}

// fakeOfferings answers by zone, two pages, and records the types asked.
type fakeOfferings struct {
	asked [][]string
	fail  bool
}

func (f *fakeOfferings) DescribeInstanceTypeOfferings(_ context.Context, in *ec2.DescribeInstanceTypeOfferingsInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	if f.fail {
		return nil, fmt.Errorf("UnauthorizedOperation")
	}
	if in.LocationType != ec2types.LocationTypeAvailabilityZone {
		return nil, fmt.Errorf("by zone, please")
	}
	if in.NextToken == nil {
		f.asked = append(f.asked, in.Filters[0].Values)
		return &ec2.DescribeInstanceTypeOfferingsOutput{NextToken: aws.String("p2"), InstanceTypeOfferings: []ec2types.InstanceTypeOffering{
			{InstanceType: "m7g.large", Location: aws.String("eu-west-1a")}}}, nil
	}
	return &ec2.DescribeInstanceTypeOfferingsOutput{InstanceTypeOfferings: []ec2types.InstanceTypeOffering{
		{InstanceType: "m6g.large", Location: aws.String("eu-west-1a")}, {InstanceType: "m6g.large", Location: aws.String("eu-west-1b")}}}, nil
}

func TestLoadOfferings(t *testing.T) {
	r := &Region{Name: "eu-west-1", Instances: []ec2types.Instance{linuxX86("i-1", "m6i.large", "eu-west-1a"), linuxX86("i-2", "c5n.large", "eu-west-1a")}}
	f := &fakeOfferings{}
	loadOfferings(context.Background(), r, f, func(string, error) { t.Error("no skip") })
	if len(f.asked) != 1 || !slices.Equal(f.asked[0], []string{"m6g.large", "m7g.large"}) {
		t.Errorf("only the twins of the candidates: %v", f.asked)
	}
	if !r.Offered["eu-west-1a"]["m7g.large"] || !r.Offered["eu-west-1b"]["m6g.large"] || r.Offered["eu-west-1b"]["m7g.large"] {
		t.Errorf("by zone, both pages: %v", r.Offered)
	}
	// Nothing to ask: nothing read, and nothing unknown.
	r = &Region{Name: "eu-west-1"}
	f = &fakeOfferings{}
	loadOfferings(context.Background(), r, f, func(string, error) { t.Error("no skip") })
	if len(f.asked) != 0 || r.Offered == nil {
		t.Errorf("no candidates: no call, an empty answer: %v %v", f.asked, r.Offered)
	}
	// A failed read: a skip, and Offered stays unknown.
	r = &Region{Name: "eu-west-1", Instances: []ec2types.Instance{linuxX86("i-1", "m5.large", "eu-west-1a")}}
	var skips []string
	loadOfferings(context.Background(), r, &fakeOfferings{fail: true}, func(what string, _ error) { skips = append(skips, what) })
	if r.Offered != nil || !slices.Equal(skips, []string{"ec2:DescribeInstanceTypeOfferings"}) {
		t.Errorf("a failed read is unknown: %v %v", r.Offered, skips)
	}
}

// An old Linux instance asks for the current types at least as big; an EMR
// node or a RHEL one asks for nothing.
func TestOfferingsForOldTypes(t *testing.T) {
	emr, rhel := linuxX86("i-emr", "m4.xlarge", "eu-west-1a"), linuxX86("i-rhel", "c4.large", "eu-west-1a")
	emr.Tags = []ec2types.Tag{{Key: aws.String("aws:elasticmapreduce:job-flow-id"), Value: aws.String("j-1")}}
	rhel.UsageOperation = aws.String("RunInstances:0010")
	r := &Region{Name: "eu-west-1", Instances: []ec2types.Instance{linuxX86("i-old", "m4.large", "eu-west-1a"), emr, rhel}}
	f := &fakeOfferings{}
	loadOfferings(context.Background(), r, f, func(string, error) { t.Error("no skip") })
	want := []string{"m6a.large", "m6i.large", "m7a.large", "m7i.large", "m8a.large", "m8i.large"}
	if len(f.asked) != 1 || !slices.Equal(f.asked[0], want) {
		t.Errorf("asked %v, want %v", f.asked, want)
	}
}
