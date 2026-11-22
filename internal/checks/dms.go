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
			Detail:   "DMS Serverless replication has been stopped or failed for at least 7 days. Unpriced because which states still incur DCU-hour charges is unverified.",
			Evidence: []string{fmt.Sprintf("DMS DescribeReplications: %s, last stopped %s UTC", x.Status, x.LastStop.UTC().Format("2006-01-02 15:04"))},
			Facts:    facts("dms_replication", x.ID, "arn", x.ARN, "state", x.Status, "type", "serverless"),
			Fix:      []string{"# Review endpoints, migration progress and restart needs before deleting the replication configuration."},
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
		state = fmt.Sprintf("all %d tasks stopped or failed for at least %d days", n, inventory.DMSIdleDays)
	}
	f := Finding{
		Check: idleDMS, Region: r.Name, Resource: i.ID, Key: i.ARN,
		Confidence: "low",
		Detail:     fmt.Sprintf("DMS %s replication instance with %s", i.Class, state),
		Why:        "an instance reserved for a cutover, a recovery drill or a task that will resume may look the same. A reserved DMS instance keeps billing until its term ends",
		Evidence:   []string{"DMS DescribeReplicationInstances and DescribeReplicationTasks: available, " + state},
		Facts:      facts("dms_instance", i.ID, "arn", i.ARN, "instance_type", i.Class, "tasks", itoa(n)),
		Fix: []string{
			"# Confirm every task can be removed or moved and save any migration progress you need before deleting the instance.",
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
		f.Detail += ". Unpriced because the instance may be covered by the DMS free tier"
	case i.MultiAZ == nil:
		f.Detail += ". Unpriced because its Availability Zone configuration is unknown"
	default:
		hour, regional, read := dmsHour(r, o, i)
		if !read {
			return Finding{}, false
		}
		if hour == 0 {
			f.Detail += ". No matching On-Demand compute price found"
			break
		}
		f.MonthlyUSD, f.Priced = hour*pricing.HoursPerMonth, true
		f.Detail += fmt.Sprintf(". $%.4f per instance-hour for compute before credits or discounts", hour)
		if !regional {
			f.Detail += " (at least, using the built-in us-east-1 rate)"
		}
	}
	return f, true
}
