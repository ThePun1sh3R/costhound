package inventory

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go"
)

type fakeKinesis struct {
	onDemand      bool
	status        types.MinimumThroughputBillingCommitmentOutputStatus
	errCode       string
	settingsCalls int
}

func (c *fakeKinesis) ListStreams(context.Context, *kinesis.ListStreamsInput, ...func(*kinesis.Options)) (*kinesis.ListStreamsOutput, error) {
	names := []string{"clicks"}
	if c.onDemand {
		names = append(names, "audit", "orders")
	}
	return &kinesis.ListStreamsOutput{StreamNames: names}, nil
}

func (c *fakeKinesis) DescribeStreamSummary(_ context.Context, in *kinesis.DescribeStreamSummaryInput, _ ...func(*kinesis.Options)) (*kinesis.DescribeStreamSummaryOutput, error) {
	mode := types.StreamModeProvisioned
	if aws.ToString(in.StreamName) != "clicks" {
		mode = types.StreamModeOnDemand
	}
	return &kinesis.DescribeStreamSummaryOutput{
		StreamDescriptionSummary: &types.StreamDescriptionSummary{
			StreamModeDetails: &types.StreamModeDetails{StreamMode: mode},
		},
	}, nil
}

func (c *fakeKinesis) DescribeAccountSettings(context.Context, *kinesis.DescribeAccountSettingsInput, ...func(*kinesis.Options)) (*kinesis.DescribeAccountSettingsOutput, error) {
	c.settingsCalls++
	if c.errCode != "" {
		return nil, &smithy.GenericAPIError{Code: c.errCode}
	}
	out := &kinesis.DescribeAccountSettingsOutput{}
	if c.status != "" {
		out.MinimumThroughputBillingCommitment = &types.MinimumThroughputBillingCommitmentOutput{Status: c.status}
	}
	return out, nil
}

func TestLoadKinesisOnDemandBilling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   types.MinimumThroughputBillingCommitmentOutputStatus
		errCode  string
		onDemand bool
		billing  KinesisOnDemandBilling
	}{
		{"enabled", types.MinimumThroughputBillingCommitmentOutputStatusEnabled, "", true, KinesisOnDemandAdvantage},
		{"enabled until earliest end", types.MinimumThroughputBillingCommitmentOutputStatusEnabledUntilEarliestAllowedEnd, "", true, KinesisOnDemandAdvantage},
		{"disabled", types.MinimumThroughputBillingCommitmentOutputStatusDisabled, "", true, KinesisOnDemandStandard},
		{"no commitment", "", "", true, KinesisOnDemandStandard},
		{"access denied", "", "AccessDeniedException", true, KinesisOnDemandBillingUnknown},
		{"throttled", "", "ThrottlingException", true, KinesisOnDemandBillingUnknown},
		{"provisioned only", "", "", false, KinesisOnDemandStandard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Region{Name: "eu-west-1"}
			c := &fakeKinesis{onDemand: tc.onDemand, status: tc.status, errCode: tc.errCode}
			if err := loadStreams(context.Background(), r, c); err != nil {
				t.Fatal(err)
			}
			wantCalls, wantStreams := 0, 1
			if tc.onDemand {
				wantCalls, wantStreams = 1, 3
			}
			if c.settingsCalls != wantCalls || len(r.Streams) != wantStreams || r.KinesisOnDemandBilling != tc.billing {
				t.Errorf("got %d settings calls, %d streams and billing %v, want %d, %d and %v",
					c.settingsCalls, len(r.Streams), r.KinesisOnDemandBilling, wantCalls, wantStreams, tc.billing)
			}
			if tc.errCode == "" {
				if len(r.Skipped) != 0 {
					t.Errorf("unexpected skips: %+v", r.Skipped)
				}
				return
			}
			want := Skip{Region: r.Name, What: "kinesis:DescribeAccountSettings", Error: tc.errCode}
			if len(r.Skipped) != 1 || r.Skipped[0] != want {
				t.Errorf("skips = %+v, want %+v", r.Skipped, want)
			}
		})
	}
}
