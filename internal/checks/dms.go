package checks

import (
	"fmt"
	"strconv"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

const idleDMS = "idle-dms-instance"

// IdleDMS finds provisioned replication instances with no tasks, or only
// tasks that have been stopped or failed for at least seven days. Serverless
// replications are reported separately, without estimating their DCU cost.
func IdleDMS(r *inventory.Region, o Options) []Finding {
	d := r.DMS
	if d == nil {
		return nil
	}
	var out []Finding
	for _, i := range d.Instances {
		if i.ARN == "" {
			continue
		}
		if f, ok := idleDMSInstance(r, o, d, i); ok {
			out = append(out, f)
		} else {
			r.Unjudged = append(r.Unjudged, idleDMS+"|"+i.ARN)
		}
	}
	for _, x := range d.Serverless {
		if x.ARN == "" {
			continue
		}
		if !inventory.DMSStopped(x.Status, x.LastStop, o.Now) {
			r.Unjudged = append(r.Unjudged, idleDMS+"|"+x.ARN)
			continue
		}
		out = append(out, Finding{
			Check: idleDMS, Region: r.Name, Resource: x.ID, Key: x.ARN,
			Detail:   fmt.Sprintf("DMS Serverless replication stopped or failed for at least %d days; not priced: AWS bills for the capacity (DCUs) a replication is using, and doesn't say whether a stopped one still holds any", inventory.DMSIdleDays),
			Why:      "a replication stopped until a cutover, or waiting for a fix after a failure, looks the same, and a stopped one may cost nothing",
			Evidence: []string{fmt.Sprintf("DMS DescribeReplications: %s, last stopped %s UTC", x.Status, x.LastStop.UTC().Format("2006-01-02 15:04"))},
			Facts:    facts("dms_replication", x.ID, "arn", x.ARN, "state", x.Status, "type", "serverless"),
			Fix:      []string{"# a person's call: delete its replication configuration only when the migration is finished or given up"},
		})
	}
	return out
}

func idleDMSInstance(r *inventory.Region, o Options, d *inventory.DMS, i inventory.DMSInstance) (Finding, bool) {
	if i.ID == "" || !d.IdleInstance(i, o.Now) {
		return Finding{}, false
	}
	n := len(d.Tasks[i.ARN])
	state := "no replication tasks"
	if n > 0 {
		state = fmt.Sprintf("%d task(s), all stopped or failed for at least %d days", n, inventory.DMSIdleDays)
	}
	f := Finding{
		Check: idleDMS, Region: r.Name, Resource: i.ID, Key: i.ARN,
		Confidence: "low",
		Detail:     fmt.Sprintf("DMS %s replication instance with %s", i.Class, state),
		Why:        "an instance kept for a cutover, a recovery drill or a task that will resume looks the same",
		Evidence:   []string{"DMS DescribeReplicationInstances and DescribeReplicationTasks: available, " + state},
		Facts:      facts("dms_instance", i.ID, "arn", i.ARN, "instance_type", i.Class, "tasks", itoa(n)),
		Fix: []string{
			"# a person's call: AWS won't delete an instance that still has tasks, so delete or move them first; a deleted task loses its place in the migration",
			fmt.Sprintf("# aws dms delete-replication-instance --region %s --replication-instance-arn %s", r.Name, i.ARN),
		},
	}
	if i.MultiAZ != nil {
		f.Facts["multi_az"] = strconv.FormatBool(*i.MultiAZ)
		if *i.MultiAZ {
			f.Detail += " (Multi-AZ)"
		}
	}
	if !dmsMetricProof(r, o, i, &f) {
		return Finding{}, false
	}
	switch {
	case i.MayBeFree(o.Now):
		f.Detail += "; not priced: it may be on the DMS free tier"
	case i.MultiAZ == nil:
		f.Detail += "; not priced: whether it's Multi-AZ couldn't be read"
	default:
		hour, regional, read := dmsHour(r, o, i)
		if !read {
			return Finding{}, false
		}
		if hour == 0 {
			f.Detail += "; no On-Demand price for its class was found, so it's unpriced"
			break
		}
		f.MonthlyUSD, f.Priced = hour*pricing.HoursPerMonth, true
		f.Detail += fmt.Sprintf("; billed $%.4f an hour On-Demand for its compute", hour)
		if !regional {
			f.Detail += " (the us-east-1 rate; most regions charge more)"
		}
	}
	return f, true
}
