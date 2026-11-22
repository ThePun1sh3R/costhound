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
	dms "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"
)

type fakeDMS struct {
	instances func(*dms.DescribeReplicationInstancesInput) (*dms.DescribeReplicationInstancesOutput, error)
	tasks     func(*dms.DescribeReplicationTasksInput) (*dms.DescribeReplicationTasksOutput, error)
	configs   func(*dms.DescribeReplicationConfigsInput) (*dms.DescribeReplicationConfigsOutput, error)
	states    func(*dms.DescribeReplicationsInput) (*dms.DescribeReplicationsOutput, error)
}

func (f fakeDMS) DescribeReplicationInstances(_ context.Context, in *dms.DescribeReplicationInstancesInput, _ ...func(*dms.Options)) (*dms.DescribeReplicationInstancesOutput, error) {
	return f.instances(in)
}

func (f fakeDMS) DescribeReplicationTasks(_ context.Context, in *dms.DescribeReplicationTasksInput, _ ...func(*dms.Options)) (*dms.DescribeReplicationTasksOutput, error) {
	return f.tasks(in)
}

func (f fakeDMS) DescribeReplicationConfigs(_ context.Context, in *dms.DescribeReplicationConfigsInput, _ ...func(*dms.Options)) (*dms.DescribeReplicationConfigsOutput, error) {
	return f.configs(in)
}

func (f fakeDMS) DescribeReplications(_ context.Context, in *dms.DescribeReplicationsInput, _ ...func(*dms.Options)) (*dms.DescribeReplicationsOutput, error) {
	return f.states(in)
}

func TestLoadDMSReadsOnlyDecisionFields(t *testing.T) {
	const discard = "discarded-sentinel"
	created, stopped := time.Unix(1760000000, 250000000), time.Unix(1760100000, 0)
	var pages []string
	c := fakeDMS{
		instances: func(in *dms.DescribeReplicationInstancesInput) (*dms.DescribeReplicationInstancesOutput, error) {
			token := aws.ToString(in.Marker)
			pages = append(pages, token)
			if token == "" {
				return &dms.DescribeReplicationInstancesOutput{Marker: aws.String("next"), ReplicationInstances: []dmstypes.ReplicationInstance{{
					ReplicationInstanceArn: aws.String("rep-a"), ReplicationInstanceIdentifier: aws.String("sync-a"),
					ReplicationInstanceClass: aws.String("dms.r5.large"), ReplicationInstanceStatus: aws.String("available"), InstanceCreateTime: &created,
					KerberosAuthenticationSettings: &dmstypes.KerberosAuthenticationSettings{Krb5FileContents: aws.String(discard)},
				}}}, nil
			}
			return &dms.DescribeReplicationInstancesOutput{ReplicationInstances: []dmstypes.ReplicationInstance{{
				ReplicationInstanceArn: aws.String("rep-b"), ReplicationInstanceIdentifier: aws.String("sync-b"), MultiAZ: true,
			}}}, nil
		},
		tasks: func(in *dms.DescribeReplicationTasksInput) (*dms.DescribeReplicationTasksOutput, error) {
			if !aws.ToBool(in.WithoutSettings) {
				t.Error("replication tasks must be requested without settings")
			}
			return &dms.DescribeReplicationTasksOutput{ReplicationTasks: []dmstypes.ReplicationTask{{
				Status: aws.String("stopped"), ReplicationInstanceArn: aws.String("rep-a"), TargetReplicationInstanceArn: aws.String("rep-b"),
				ReplicationTaskStats:    &dmstypes.ReplicationTaskStats{StopDate: &stopped},
				ReplicationTaskSettings: aws.String(discard), LastFailureMessage: aws.String(discard),
			}}}, nil
		},
		configs: func(*dms.DescribeReplicationConfigsInput) (*dms.DescribeReplicationConfigsOutput, error) {
			return &dms.DescribeReplicationConfigsOutput{ReplicationConfigs: []dmstypes.ReplicationConfig{
				{ReplicationConfigArn: aws.String("config-a"), ReplicationConfigIdentifier: aws.String("retired"), ReplicationSettings: aws.String(discard)},
				{ReplicationConfigArn: aws.String("config-b"), ReplicationConfigIdentifier: aws.String("never-started")},
			}}, nil
		},
		states: func(*dms.DescribeReplicationsInput) (*dms.DescribeReplicationsOutput, error) {
			return &dms.DescribeReplicationsOutput{Replications: []dmstypes.Replication{{
				ReplicationConfigArn: aws.String("config-a"), Status: aws.String("stopped"), ReplicationLastStopTime: &stopped, FailureMessages: []string{discard},
			}}}, nil
		},
	}
	r := &Region{}
	loadDMS(t.Context(), r, c, func(op string, _ error) { t.Errorf("unexpected skip %s", op) })
	d := r.DMS
	if !slices.Equal(pages, []string{"", "next"}) || len(d.Instances) != 2 || len(d.Serverless) != 2 {
		t.Fatalf("incomplete DMS inventory, pages = %v", pages)
	}
	if !d.Instances[0].Created.Equal(created) || !aws.ToBool(d.Instances[1].MultiAZ) ||
		!d.Tasks["rep-a"][0].StopDate.Equal(stopped) || d.IdleInstance(d.Instances[0], stopped) ||
		len(d.Tasks["rep-b"]) != 1 || d.Tasks["rep-b"][0].Status != "" ||
		d.Serverless[0].Status != "stopped" || d.Serverless[1].Status != "" {
		t.Fatal("incorrect task stop date, pending move or Serverless state")
	}
	encoded, err := json.Marshal(d)
	if err != nil || strings.Contains(string(encoded), discard) {
		t.Fatal("DMS settings or failure text survived loading")
	}
}

func TestLoadDMSFailedReadIsUnknown(t *testing.T) {
	c := fakeDMS{
		instances: func(*dms.DescribeReplicationInstancesInput) (*dms.DescribeReplicationInstancesOutput, error) {
			return &dms.DescribeReplicationInstancesOutput{ReplicationInstances: []dmstypes.ReplicationInstance{
				{ReplicationInstanceArn: aws.String("rep-1"), ReplicationInstanceIdentifier: aws.String("sync-a")},
			}}, nil
		},
		tasks: func(*dms.DescribeReplicationTasksInput) (*dms.DescribeReplicationTasksOutput, error) {
			return nil, errors.New("tasks denied")
		},
		configs: func(*dms.DescribeReplicationConfigsInput) (*dms.DescribeReplicationConfigsOutput, error) {
			return &dms.DescribeReplicationConfigsOutput{ReplicationConfigs: []dmstypes.ReplicationConfig{{ReplicationConfigArn: aws.String("config-1")}}}, nil
		},
		states: func(*dms.DescribeReplicationsInput) (*dms.DescribeReplicationsOutput, error) {
			return nil, errors.New("states denied")
		},
	}
	r := &Region{}
	var skips []string
	loadDMS(t.Context(), r, c, func(op string, _ error) { skips = append(skips, op) })
	if r.DMS.Tasks != nil || len(r.DMS.Serverless) != 1 || r.DMS.Serverless[0].Status != "" {
		t.Fatal("failed DMS reads were treated as complete")
	}
	if !slices.Equal(skips, []string{"dms:DescribeReplicationTasks", "dms:DescribeReplications"}) {
		t.Errorf("wrong skips: %v", skips)
	}
}

func TestLoadDMSRecordsMissingInstanceKeys(t *testing.T) {
	c := fakeDMS{
		instances: func(*dms.DescribeReplicationInstancesInput) (*dms.DescribeReplicationInstancesOutput, error) {
			return &dms.DescribeReplicationInstancesOutput{ReplicationInstances: []dmstypes.ReplicationInstance{
				{ReplicationInstanceIdentifier: aws.String("no-arn")}, {ReplicationInstanceArn: aws.String("no-id")},
			}}, nil
		},
		tasks: func(*dms.DescribeReplicationTasksInput) (*dms.DescribeReplicationTasksOutput, error) {
			return &dms.DescribeReplicationTasksOutput{}, nil
		},
		configs: func(*dms.DescribeReplicationConfigsInput) (*dms.DescribeReplicationConfigsOutput, error) {
			return &dms.DescribeReplicationConfigsOutput{}, nil
		},
	}
	r := &Region{}
	var skips []string
	loadDMS(t.Context(), r, c, func(op string, _ error) { skips = append(skips, op) })
	if !slices.Equal(skips, []string{"dms:DescribeReplicationInstances", "dms:DescribeReplicationInstances"}) ||
		len(r.DMS.Instances) != 1 || r.DMS.Instances[0].ARN != "no-id" {
		t.Fatalf("expected skips for both malformed rows and the known ARN to park: %v, %+v", skips, r.DMS.Instances)
	}
}
