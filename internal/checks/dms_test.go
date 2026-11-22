package checks

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/aws/aws-sdk-go-v2/aws"
)

var addMigrationUsageFixture = func(*inventory.Region) {}

func dmsFixture() (*inventory.Region, inventory.DMSInstance) {
	i := inventory.DMSInstance{
		ARN: "arn:aws:dms:eu-west-1:123456789012:rep:EXAMPLE",
		ID:  "old-sync", Class: "dms.r5.large", Status: "available", MultiAZ: aws.Bool(false),
	}
	i.Created = now.AddDate(0, 0, -40)
	r := &inventory.Region{Name: "eu-west-1", DMS: &inventory.DMS{
		Instances: []inventory.DMSInstance{i}, Tasks: map[string][]inventory.DMSTask{},
	}}
	return r, i
}

func TestIdleDMSUsesStopTimeAndBuiltInRates(t *testing.T) {
	for _, tc := range []struct {
		name, status, class string
		stopAge             time.Duration
		noTasks, multiAZ    bool
		freeUntil           bool
		usd                 float64
		wantFinding         bool
	}{
		{name: "no tasks", class: "dms.r5.large", noTasks: true, usd: 128.48, wantFinding: true},
		{name: "stopped a week", status: "stopped", class: "dms.r5.large", stopAge: 7 * 24 * time.Hour, usd: 128.48, wantFinding: true},
		{name: "failed Multi-AZ", status: "failed", class: "dms.r5.large", stopAge: 8 * 24 * time.Hour, multiAZ: true, usd: 257.69, wantFinding: true},
		{name: "stopped recently", status: "stopped", stopAge: 7*24*time.Hour - time.Second},
		{name: "unknown stop time", status: "failed"},
		{name: "running again", status: "running", stopAge: 10 * 24 * time.Hour},
		{name: "free tier", class: "dms.t3.micro", noTasks: true, wantFinding: true},
		{name: "Multi-AZ micro bills", class: "dms.t3.micro", noTasks: true, multiAZ: true, usd: 27.156, wantFinding: true},
		{name: "FreeUntil active", class: "dms.r5.large", noTasks: true, freeUntil: true, wantFinding: true},
		{name: "unknown class", class: "dms.future.large", noTasks: true, wantFinding: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, i := dmsFixture()
			i.MultiAZ, i.Class = aws.Bool(tc.multiAZ), tc.class
			if tc.freeUntil {
				i.FreeUntil = now.AddDate(0, 0, 1)
			}
			r.DMS.Instances[0] = i
			if !tc.noTasks {
				task := inventory.DMSTask{Status: tc.status}
				if tc.stopAge != 0 {
					task.StopDate = now.Add(-tc.stopAge)
				}
				r.DMS.Tasks[i.ARN] = []inventory.DMSTask{task}
			}
			got := IdleDMS(r, opts())
			if !tc.wantFinding {
				if len(got) != 0 || !slices.Equal(r.Unjudged, []string{idleDMS + "|" + i.ARN}) {
					t.Fatalf("wanted only a parked instance, got %+v, %v", got, r.Unjudged)
				}
				return
			}
			if len(got) != 1 || got[0].Key != i.ARN || len(r.Unjudged) != 0 {
				t.Fatalf("wanted one finding, got %+v, %v", got, r.Unjudged)
			}
			if tc.usd > 0 {
				wantUSD(t, got[0], tc.usd)
				if !strings.Contains(got[0].Detail, "at least") || got[0].Confidence != "low" {
					t.Errorf("free evidence or price basis is wrong: %+v", got[0])
				}
			} else if got[0].Priced {
				t.Errorf("wanted an unpriced finding, got %+v", got[0])
			}
		})
	}
}

func TestIdleDMSMissingTasksAndServerlessState(t *testing.T) {
	r, i := dmsFixture()
	stopped := inventory.DMSReplication{ID: "old-sync-serverless", ARN: "config-known", Status: "stopped"}
	stopped.LastStop = now.AddDate(0, 0, -9)
	r.DMS.Tasks = nil
	r.DMS.Serverless = []inventory.DMSReplication{stopped, {ARN: "config-state-unknown"}}
	got := IdleDMS(r, opts())
	if len(got) != 1 || got[0].Key != stopped.ARN || got[0].Priced || !strings.Contains(got[0].Detail, "unverified") {
		t.Fatalf("wanted only the known stopped and unpriced Serverless replication, got %+v", got)
	}
	if !slices.Equal(r.Unjudged, []string{idleDMS + "|" + i.ARN, idleDMS + "|config-state-unknown"}) {
		t.Errorf("failed reads must park known resources: %v", r.Unjudged)
	}
}
