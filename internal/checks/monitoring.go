package checks

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// monitoringMetrics is how many one-minute metrics detailed monitoring
// publishes an instance: AWS's pricing example; the count depends on the type.
const monitoringMetrics = 7

// monitoringPrice is a detailed-monitoring metric's price a month: the
// custom-metric tier past 10,000 metrics (CW:MetricMonitorUsage, us-east-1
// offer 2026-11-19). Tiers count the whole bill, every account of a
// consolidated billing family, which one account's own count doesn't show:
// the $0.30 below 10,000 is never assumed.
const monitoringPrice = 0.10

// launchers are what launch an instance and replace it, set up the same way,
// under a new ID, by the tag they put on it. Such an instance is judged with
// the rest of its launcher's (a finding per instance would be booked as
// fixed at every replacement), and the fix is the launcher's setting. An
// Auto Scaling group comes first: EKS managed node groups and Elastic
// Beanstalk environments run one.
var launchers = []struct{ tag, kind, label string }{
	{"aws:autoscaling:groupName", "asg", "Auto Scaling group"},
	{"karpenter.sh/nodepool", "karpenter", "Karpenter node pool"},
	{"aws:ec2:fleet-id", "ec2-fleet", "EC2 Fleet"},
	{"aws:ec2spot:fleet-request-id", "spot-fleet", "Spot Fleet"},
	{"aws:elasticmapreduce:job-flow-id", "emr", "EMR cluster"},
	{"aws:batch:compute-environment", "batch", "Batch compute environment"},
}

// launcherOf is the launcher an instance's tags name: its kind and name.
func launcherOf(tags []ec2types.Tag) (kind, name string) {
	for _, l := range launchers {
		if v, ok := tag(tags, l.tag); ok && v != "" {
			return l.kind, v
		}
	}
	return "", ""
}

func launcherLabel(kind string) string {
	for _, l := range launchers {
		if l.kind == kind {
			return l.label
		}
	}
	return kind
}

// DetailedMonitoring finds running EC2 instances with detailed monitoring
// on. It publishes about 7 one-minute metrics an instance, billed as custom
// metrics, prorated by the hour. Priced at $0.10 a metric until
// MonitoringTier, once the regions are joined, takes the account's free 10
// metrics once (and a lower tier past 250,000). The instances of one
// launcher (launchers) are one finding: an Auto Scaling group's launch
// template or configuration, a Karpenter node pool's EC2NodeClass, a fleet's
// launch template turns monitoring back on at the next launch. A listed
// launcher with no running instance is parked (its setting may still turn
// monitoring on at the next scale-out); a stopped instance is parked by
// ParkedWhenStopped. EMR needs no list: a live cluster always runs its
// primary node, so no running instance means terminated or starting.
// Karpenter node pools are Kubernetes objects (no AWS list): parked on the
// diff side while their EKS cluster still exists (ParkedWhileClusterExists).
func DetailedMonitoring(r *inventory.Region, o Options) []Finding {
	scaling, minute := monitoringAlarms(r)
	alarmsRead := !slices.ContainsFunc(r.Skipped, func(s inventory.Skip) bool { return s.What == "cloudwatch:DescribeAlarms" })
	groups := map[string][]ec2types.Instance{} // "kind/name" -> its monitored instances
	var order []string
	var out []Finding
	running := map[string]bool{} // "kind/name" with a running instance
	for _, in := range r.Instances {
		if in.State == nil || in.State.Name != ec2types.InstanceStateNameRunning {
			continue
		}
		kind, name := launcherOf(in.Tags)
		if kind != "" {
			running[kind+"/"+name] = true
		}
		if in.Monitoring == nil || in.Monitoring.State != ec2types.MonitoringStateEnabled {
			continue
		}
		if kind != "" {
			g := kind + "/" + name
			if _, seen := groups[g]; !seen {
				order = append(order, g)
			}
			groups[g] = append(groups[g], in)
			continue
		}
		out = append(out, monitoringFinding(r, []ec2types.Instance{in}, "", "", scaling, minute, alarmsRead))
	}
	for _, g := range order {
		kind, name, _ := strings.Cut(g, "/")
		out = append(out, monitoringFinding(r, groups[g], kind, name, scaling, minute, alarmsRead))
	}
	park := func(key string) {
		r.Unjudged = append(r.Unjudged, "ec2-detailed-monitoring|"+key)
	}
	for _, g := range r.ASGNames {
		if !running["asg/"+g] { // no running instance to judge: its template may still turn it on
			park("asg/" + g)
		}
	}
	// EMR: no new read. A live cluster always runs its primary node: no
	// running instance means the cluster is terminated or still starting.
	if r.EC2Fleets != nil {
		for _, f := range r.EC2Fleets {
			if f.Type == "instant" {
				continue // an instant fleet never launches again
			}
			switch f.State {
			case "submitted", "active", "modifying":
				if !running["ec2-fleet/"+f.ID] {
					park("ec2-fleet/" + f.ID)
				}
			}
		}
	}
	if r.SpotFleets != nil {
		for _, f := range r.SpotFleets {
			switch f.State {
			case "submitted", "active", "modifying":
				if !running["spot-fleet/"+f.ID] {
					park("spot-fleet/" + f.ID)
				}
			}
		}
	}
	if r.BatchCEs != nil {
		for _, ce := range r.BatchCEs {
			if ce.Type != "MANAGED" || (ce.ResourceType != "EC2" && ce.ResourceType != "SPOT") {
				continue
			}
			if ce.State != "ENABLED" && ce.State != "DISABLED" {
				continue
			}
			if ce.Status == "DELETING" || ce.Status == "DELETED" {
				continue
			}
			if !running["batch/"+ce.Name] && !running["batch/"+ce.ARN] {
				// The instance tag may hold the name or the ARN: park both;
				// KeepParked keeps only the one a finding had.
				park("batch/" + ce.Name)
				if ce.ARN != "" && ce.ARN != ce.Name {
					park("batch/" + ce.ARN)
				}
			}
		}
	}
	return out
}

// eksClusterOf is the EKS cluster name an instance's tags name: the name in
// a kubernetes.io/cluster/<name> tag key, else the eks:eks-cluster-name value.
func eksClusterOf(tags []ec2types.Tag) string {
	for _, t := range tags {
		if name, ok := strings.CutPrefix(aws.ToString(t.Key), "kubernetes.io/cluster/"); ok && name != "" {
			return name
		}
	}
	if v, ok := tag(tags, "eks:eks-cluster-name"); ok && v != "" {
		return v
	}
	return ""
}

// monitoringAlarms: the groups with scaling-policy alarms (one-minute
// metrics make them react faster), and what an alarm reads that only
// detailed monitoring sends: a period under 5 minutes on an instance or a
// group ("<instance ID>", "asg/<group>"), or, at any period, the
// InstanceType or ImageId aggregate ("type/<type>", "image/<AMI>"), which
// exist only for instances with detailed monitoring on -> the alarm's name.
// Metric-math alarms count by the metrics they read.
func monitoringAlarms(r *inventory.Region) (scaling map[string]bool, minute map[string]string) {
	scaling, minute = map[string]bool{}, map[string]string{}
	for _, a := range r.Alarms {
		stats := append([]inventory.AlarmStat{{Namespace: a.Namespace, Dimensions: a.Dimensions, Period: a.Period}}, a.Stats...)
		for _, s := range stats {
			g := s.Dimensions["AutoScalingGroupName"]
			if a.Scaling && g != "" {
				scaling[g] = true
			}
			if s.Namespace != "AWS/EC2" {
				continue
			}
			watch := func(key string) {
				if minute[key] == "" {
					minute[key] = a.Name
				}
			}
			if v := s.Dimensions["InstanceType"]; v != "" {
				watch("type/" + v)
			}
			if v := s.Dimensions["ImageId"]; v != "" {
				watch("image/" + v)
			}
			if s.Period <= 0 || s.Period >= 300 {
				continue
			}
			if id := s.Dimensions["InstanceId"]; id != "" {
				watch(id)
			}
			if g != "" {
				watch("asg/" + g)
			}
		}
	}
	return scaling, minute
}

// launchText names what launches a group's instances.
func launchText(l inventory.LaunchSource) string {
	switch {
	case l.Template != "":
		return fmt.Sprintf("launch template %s (%s), version %s", l.Template, l.TemplateID, l.Version)
	case l.Config != "":
		return "launch configuration " + l.Config + " (it can't be changed: move the group to a launch template with detailed monitoring off)"
	}
	return "launch template or configuration"
}

func monitoringFinding(r *inventory.Region, ins []ec2types.Instance, kind, group string, scaling map[string]bool, minute map[string]string, alarmsRead bool) Finding {
	n, metrics := len(ins), monitoringMetrics*len(ins)
	var ids, cats []string
	watcher := ""
	if kind == "asg" {
		watcher = minute["asg/"+group]
	}
	for _, in := range ins {
		id := aws.ToString(in.InstanceId)
		ids = append(ids, id)
		for _, k := range []string{id, "type/" + string(in.InstanceType), "image/" + aws.ToString(in.ImageId)} {
			if watcher == "" {
				watcher = minute[k]
			}
		}
		for _, c := range in.Monitoring.MetricCategoryIds {
			if !slices.Contains(cats, c) {
				cats = append(cats, c)
			}
		}
	}
	slices.Sort(cats)
	first := ins[0]
	f := Finding{Check: "ec2-detailed-monitoring", Region: r.Name, Confidence: "medium",
		Why: "something may read its one-minute metrics: a dashboard, an alarm or faster Auto Scaling"}
	usd := float64(metrics) * monitoringPrice
	unread := ""
	if !alarmsRead {
		unread = "yes"
	}
	if kind == "" {
		id := ids[0]
		f.Resource, f.Key = label(id, first.Tags), id
		f.Facts = facts("instance", id, "instance_type", string(first.InstanceType), "metrics", itoa(metrics), "categories", strings.Join(cats, " "),
			"minute_alarm", watcher, "alarms_unread", unread)
		f.Detail = "detailed monitoring on: about 7 one-minute metrics, billed as custom metrics"
		f.Fix = []string{fmt.Sprintf("aws ec2 unmonitor-instances --region %s --instance-ids %s", r.Name, id)}
	} else {
		f.Resource, f.Key, f.Members = group+" ("+launcherLabel(kind)+")", kind+"/"+group, ids
		f.Detail = fmt.Sprintf("detailed monitoring on for %d instance(s) of one %s: about %d one-minute metrics, billed as custom metrics", n, launcherLabel(kind), metrics)
		f.Facts = facts("launcher", kind, "instances", itoa(n), "sample", ids[0], "metrics", itoa(metrics), "categories", strings.Join(cats, " "), "minute_alarm", watcher, "alarms_unread", unread)
		switch kind {
		case "asg":
			l := r.ASGLaunch[group]
			for k, v := range facts("asg", group, "launch_template", l.Template, "launch_template_id", l.TemplateID, "launch_version", l.Version, "launch_configuration", l.Config) {
				f.Facts[k] = v
			}
			step := "add a version with detailed monitoring off and point the group at it"
			if l.Template == "" && l.Config != "" {
				step = "the group needs a launch template instead"
			}
			f.Fix = []string{
				fmt.Sprintf("# its %s turns it on at every launch: %s", launchText(l), step),
				fmt.Sprintf("# then aws autoscaling start-instance-refresh --region %s --auto-scaling-group-name %s, or let instances cycle", r.Name, group),
			}
			if scaling[group] {
				f.Confidence = "low"
				f.Why = "the group scales on alarms, and one-minute metrics make it react faster; " + f.Why
			}
		default:
			f.Facts["launched_by"], f.Facts["group"] = kind, group
			if kind == "karpenter" {
				if cluster := eksClusterOf(first.Tags); cluster != "" {
					f.Facts["eks_cluster"] = cluster
				}
			}
			f.Fix = []string{map[string]string{
				"karpenter":  "# Karpenter node pool " + group + " launches it: set spec.detailedMonitoring: false in its EC2NodeClass, and the nodes it launches next come without it",
				"ec2-fleet":  "# EC2 Fleet " + group + " launches it: turn detailed monitoring off in the launch template it uses, for the instances it launches next",
				"spot-fleet": "# Spot Fleet " + group + " launches it: turn detailed monitoring off in its launch template or launch specification, for the instances it launches next",
				"emr":        "# EMR cluster " + group + " launched it and replaces its instances when it resizes: turn detailed monitoring off where its instances get it (a custom AMI or a bootstrap action)",
				"batch":      "# Batch compute environment " + group + " launches it: turn detailed monitoring off in its launch template, for the instances it launches next",
			}[kind]}
		}
	}
	if owner := monitoringOwner(first.Tags); owner != "" {
		f.Fix = append([]string{"# " + owner + " launches it: turn detailed monitoring off there, or it comes back"}, f.Fix...)
	}
	if watcher != "" {
		f.Confidence = "low"
		f.Why = "an alarm reads what only detailed monitoring sends (a period under 5 minutes, or the InstanceType or ImageId aggregate): after the switch it finds no data; " + f.Why
	}
	if len(cats) > 0 {
		f.Confidence = "low"
		f.Why = "it also sends metric categories (" + strings.Join(cats, ", ") + "), which turning detailed monitoring off drops too; " + f.Why
	}
	if !alarmsRead {
		f.Confidence = "low"
		f.Why = "the alarms couldn't be read, and one may watch its one-minute metrics; " + f.Why
	}
	if f.Confidence == "low" && !strings.HasPrefix(f.Fix[len(f.Fix)-1], "# ") {
		f.Fix[len(f.Fix)-1] = "# " + f.Fix[len(f.Fix)-1]
	}
	f.MonthlyUSD, f.Priced = usd, true
	f.Detail += "; " + monitoringPriced(usd, monitoringPrice, 0)
	f.Evidence = []string{fmt.Sprintf("EC2 DescribeInstances: %d running instance(s) with Monitoring enabled", n)}
	return f
}

// monitoringOwner names the service that launches the instance, from its tags.
func monitoringOwner(tags []ec2types.Tag) string {
	if ng, ok := tag(tags, "eks:nodegroup-name"); ok && ng != "" {
		return "EKS managed node group " + ng + " (its launch template)"
	}
	if env, ok := tag(tags, "elasticbeanstalk:environment-name"); ok && env != "" {
		return "Elastic Beanstalk environment " + env + " (its configuration)"
	}
	return ""
}

func monitoringPriced(usd, price float64, free int) string {
	s := fmt.Sprintf("about $%.2f a month at $%.2f a metric (the price past 10,000 metrics in the bill; up to $0.30 below that, less past 250,000)", usd, price)
	if free > 0 {
		s += fmt.Sprintf(", %d of its metrics in the account's first 10, which are free", free)
	}
	return s
}

// MonitoringTier prices detailed monitoring once the regions are joined.
// The account's first 10 metrics are free, counted once: by its custom
// metrics first when their count is known (paid plans read it), by these
// otherwise (the safe side). A known count only lowers the price (past
// 250,000 metrics): monitoringPrice says why it never raises it.
func MonitoringTier(fs []Finding, custom int, known bool) {
	total := 0
	for _, f := range fs {
		if f.Check == "ec2-detailed-monitoring" {
			m, _ := strconv.Atoi(f.Facts["metrics"])
			total += m
		}
	}
	price, free := monitoringPrice, 10
	if known {
		price, free = knownMonitoringTier(custom, total)
	}
	var order []int // by region and key: the free 10 land on the same findings every run
	for i := range fs {
		if fs[i].Check == "ec2-detailed-monitoring" {
			order = append(order, i)
		}
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Or(strings.Compare(fs[a].Region, fs[b].Region), strings.Compare(fs[a].Key, fs[b].Key))
	})
	for _, i := range order {
		f := &fs[i]
		m, _ := strconv.Atoi(f.Facts["metrics"])
		take := min(m, free)
		free -= take
		usd := float64(m-take) * price
		f.Detail = strings.Replace(f.Detail, monitoringPriced(f.MonthlyUSD, monitoringPrice, 0), monitoringPriced(usd, price, take), 1)
		f.MonthlyUSD = usd
	}
}
