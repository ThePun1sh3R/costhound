package inventory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	dms "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
)

const DMSIdleDays = 7

// DMS holds only identifiers, configuration relevant to pricing, and task
// state. A nil task map means the tasks could not be read.
type DMS struct {
	Instances  []DMSInstance
	Tasks      map[string][]DMSTask // instance ARN -> every task using or moving to it
	Serverless []DMSReplication
}

type DMSInstance struct {
	ARN, ID, Class, Status string
	MultiAZ                *bool
	Created, FreeUntil     time.Time
}

type DMSTask struct {
	Status   string
	StopDate time.Time
}

type DMSReplication struct {
	ARN, ID, Status string
	LastStop        time.Time
}

// DMSAPI is the DMS inventory used by the idle replication check.
type DMSAPI interface {
	dms.DescribeReplicationInstancesAPIClient
	dms.DescribeReplicationTasksAPIClient
	dms.DescribeReplicationConfigsAPIClient
	dms.DescribeReplicationsAPIClient
}

func loadDMS(ctx context.Context, r *Region, c DMSAPI, skip func(string, error)) {
	d := &DMS{}
	r.DMS = d
	const op = "dms:DescribeReplicationInstances"
	var instances []DMSInstance
	p := dms.NewDescribeReplicationInstancesPaginator(c, &dms.DescribeReplicationInstancesInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *dms.DescribeReplicationInstancesOutput) {
		for _, x := range o.ReplicationInstances {
			arn, id := aws.ToString(x.ReplicationInstanceArn), aws.ToString(x.ReplicationInstanceIdentifier)
			if arn == "" || id == "" {
				skip(op, errors.New("a listed replication instance is missing its ARN or identifier"))
			}
			if arn == "" {
				continue
			}
			instances = append(instances, DMSInstance{
				ARN: arn, ID: id, Class: aws.ToString(x.ReplicationInstanceClass), Status: aws.ToString(x.ReplicationInstanceStatus),
				MultiAZ: aws.Bool(x.MultiAZ), Created: aws.ToTime(x.InstanceCreateTime), FreeUntil: aws.ToTime(x.FreeUntil),
			})
		}
	}); err != nil {
		skip(op, err)
	} else if len(instances) > 0 {
		d.Instances = instances
		loadDMSTasks(ctx, d, c, skip)
	}
	loadDMSServerless(ctx, d, c, skip)
}

func loadDMSTasks(ctx context.Context, d *DMS, c dms.DescribeReplicationTasksAPIClient, skip func(string, error)) {
	const op = "dms:DescribeReplicationTasks"
	byInstance := map[string][]DMSTask{}
	missingARN := false
	p := dms.NewDescribeReplicationTasksPaginator(c, &dms.DescribeReplicationTasksInput{WithoutSettings: aws.Bool(true)})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *dms.DescribeReplicationTasksOutput) {
		for _, t := range o.ReplicationTasks {
			source, target := aws.ToString(t.ReplicationInstanceArn), aws.ToString(t.TargetReplicationInstanceArn)
			if source == "" {
				missingARN = true
				continue
			}
			task := DMSTask{Status: aws.ToString(t.Status)}
			if t.ReplicationTaskStats != nil {
				task.StopDate = aws.ToTime(t.ReplicationTaskStats.StopDate)
			}
			byInstance[source] = append(byInstance[source], task)
			if target != "" && target != source {
				// A pending or failed move is still a dependency of the target.
				byInstance[target] = append(byInstance[target], DMSTask{})
			}
		}
	}); err != nil {
		skip(op, err)
	} else if missingARN {
		skip(op, errors.New("a replication task has no instance ARN"))
	} else {
		d.Tasks = byInstance
	}
}

func loadDMSServerless(ctx context.Context, d *DMS, c DMSAPI, skip func(string, error)) {
	const configs, replications = "dms:DescribeReplicationConfigs", "dms:DescribeReplications"
	var list []DMSReplication
	p := dms.NewDescribeReplicationConfigsPaginator(c, &dms.DescribeReplicationConfigsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *dms.DescribeReplicationConfigsOutput) {
		for _, x := range o.ReplicationConfigs {
			arn, id := aws.ToString(x.ReplicationConfigArn), aws.ToString(x.ReplicationConfigIdentifier)
			if arn == "" {
				skip(configs, errors.New("a listed replication configuration is missing its ARN"))
				continue
			}
			if id == "" { // listed without its identifier: name it by its ARN's resource ID
				id = arn[strings.LastIndex(arn, ":")+1:]
			}
			list = append(list, DMSReplication{ARN: arn, ID: id})
		}
	}); err != nil {
		skip(configs, err)
		return
	}
	if len(list) == 0 {
		return
	}
	byARN := map[string]DMSReplication{}
	statePages := dms.NewDescribeReplicationsPaginator(c, &dms.DescribeReplicationsInput{})
	if err := drain(ctx, statePages.HasMorePages, statePages.NextPage, func(o *dms.DescribeReplicationsOutput) {
		for _, x := range o.Replications {
			byARN[aws.ToString(x.ReplicationConfigArn)] = DMSReplication{
				Status: aws.ToString(x.Status), LastStop: aws.ToTime(x.ReplicationLastStopTime),
			}
		}
	}); err != nil {
		skip(replications, err)
	} else {
		for i := range list {
			s := byARN[list[i].ARN]
			list[i].Status, list[i].LastStop = s.Status, s.LastStop
		}
	}
	d.Serverless = list
}

func (d *DMS) IdleInstance(i DMSInstance, now time.Time) bool {
	if d.Tasks == nil || i.Status != "available" || i.Created.IsZero() || now.Sub(i.Created) < DMSIdleDays*24*time.Hour {
		return false
	}
	for _, t := range d.Tasks[i.ARN] {
		if !DMSStopped(t.Status, t.StopDate, now) {
			return false
		}
	}
	return true
}

func DMSStopped(status string, stop, now time.Time) bool {
	return (status == "stopped" || status == "failed") && !stop.IsZero() && now.Sub(stop) >= DMSIdleDays*24*time.Hour
}

func (i DMSInstance) MayBeFree(now time.Time) bool {
	return i.FreeUntil.After(now) || i.Class == "dms.t3.micro" && i.MultiAZ != nil && !*i.MultiAZ
}
