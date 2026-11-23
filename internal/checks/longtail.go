package checks

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"strconv"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/pricing"
)

// UnusedAMIs finds your AMIs that nothing launches: no non-terminated
// instance runs from one, EC2 knows of no launch template, SSM parameter or
// Image Builder recipe that names it, nothing launched from it within
// -snapshot-age days, and it isn't public or made by AWS Backup or DLM.
//
// Snapshots are incremental, so AMIs are grouped by the disks their
// snapshots came from: the money comes back only when every snapshot of
// those disks goes, so a group is priced (at each disk's largest snapshot)
// only when all of them belong to the group's AMIs. old-snapshots leaves AMI
// snapshots alone, so nothing is counted twice.
func UnusedAMIs(r *inventory.Region, o Options) []Finding {
	for _, s := range r.Skipped {
		if s.What == "ec2:DescribeInstances" {
			return nil // which AMIs run is unknown: don't guess
		}
	}
	used := map[string]bool{}
	for _, in := range r.Instances {
		if in.State == nil || in.State.Name != ec2types.InstanceStateNameTerminated {
			used[aws.ToString(in.ImageId)] = true
		}
	}
	var cands []ec2types.Image
	isCand := map[string]bool{}
	for _, img := range r.Images {
		id := aws.ToString(img.ImageId)
		if used[id] || r.ImageRefs[id] || aws.ToBool(img.Public) || policyImage(img) {
			continue
		}
		if img.State != ec2types.ImageStateAvailable && img.State != ec2types.ImageStateDisabled {
			continue
		}
		if days(o.Now, parseTime(aws.ToString(img.CreationDate))) < o.SnapshotAgeDays {
			continue
		}
		if last := parseTime(aws.ToString(img.LastLaunchedTime)); last != nil && days(o.Now, last) < o.SnapshotAgeDays {
			continue // launched recently, by something costhound can't see
		}
		cands = append(cands, img)
		isCand[id] = true
	}
	if len(cands) == 0 {
		return nil
	}

	snaps := map[string]ec2types.Snapshot{}
	for _, s := range r.Snapshots {
		snaps[aws.ToString(s.SnapshotId)] = s
	}
	// lineage: the disk a snapshot came from; copies (vol-ffffffff) stand alone.
	lineage := func(snapID string) string {
		if s, ok := snaps[snapID]; ok {
			if v := aws.ToString(s.VolumeId); v != "" && v != "vol-ffffffff" {
				return v
			}
		}
		return "snap:" + snapID
	}
	keptBy := map[string]bool{} // lineages a non-candidate AMI uses
	for _, img := range r.Images {
		if isCand[aws.ToString(img.ImageId)] {
			continue
		}
		for _, bd := range img.BlockDeviceMappings {
			if bd.Ebs != nil && bd.Ebs.SnapshotId != nil {
				keptBy[lineage(*bd.Ebs.SnapshotId)] = true
			}
		}
	}

	// Group candidate AMIs that share a lineage (union-find).
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	owner := map[string]string{} // lineage -> first AMI using it
	amiSnaps := map[string][]string{}
	mapGiB := map[string]float64{} // lineage -> the AMI's own size for it, when the snapshot isn't listed
	for _, img := range cands {
		id := aws.ToString(img.ImageId)
		parent[id] = id
		for _, bd := range img.BlockDeviceMappings {
			if bd.Ebs == nil || bd.Ebs.SnapshotId == nil {
				continue
			}
			sid := *bd.Ebs.SnapshotId
			amiSnaps[id] = append(amiSnaps[id], sid)
			l := lineage(sid)
			mapGiB[l] = max(mapGiB[l], float64(aws.ToInt32(bd.Ebs.VolumeSize)))
			if o, ok := owner[l]; ok {
				parent[find(id)] = find(o)
			} else {
				owner[l] = id
			}
		}
	}
	type group struct {
		amis     []ec2types.Image
		lineages map[string]bool
	}
	groups := map[string]*group{}
	var order []string
	for _, img := range cands {
		root := find(aws.ToString(img.ImageId))
		g, ok := groups[root]
		if !ok {
			g = &group{lineages: map[string]bool{}}
			groups[root] = g
			order = append(order, root)
		}
		g.amis = append(g.amis, img)
		for _, sid := range amiSnaps[aws.ToString(img.ImageId)] {
			g.lineages[lineage(sid)] = true
		}
	}

	// Every snapshot of a disk, to see whether a group's AMIs hold all of them.
	byDisk := map[string][]ec2types.Snapshot{}
	for _, s := range r.Snapshots {
		byDisk[lineage(aws.ToString(s.SnapshotId))] = append(byDisk[lineage(aws.ToString(s.SnapshotId))], s)
	}

	var out []Finding
	for _, root := range order {
		g := groups[root]
		inGroup := map[string]bool{}
		var ids, snapIDs []string
		for _, img := range g.amis {
			ids = append(ids, aws.ToString(img.ImageId))
			for _, sid := range amiSnaps[aws.ToString(img.ImageId)] {
				inGroup[sid] = true
				snapIDs = append(snapIDs, sid)
			}
		}
		var lins []string
		for l := range g.lineages {
			lins = append(lins, l)
		}
		sort.Strings(lins)
		cost, gib, whole := 0.0, 0.0, true
		for _, l := range lins {
			largest, price := 0.0, pb(r).SnapshotGB
			for _, s := range byDisk[l] {
				if !inGroup[aws.ToString(s.SnapshotId)] {
					whole = false // another snapshot of this disk stays
				}
				size := float64(aws.ToInt32(s.VolumeSize))
				if s.FullSnapshotSizeInBytes != nil && *s.FullSnapshotSizeInBytes > 0 {
					size = float64(*s.FullSnapshotSizeInBytes) / bytesPerGiB
				}
				if size > largest {
					largest = size
					price = pb(r).SnapshotGB
					if s.StorageTier == ec2types.StorageTierArchive {
						price = pb(r).SnapshotArchiveGB
					}
				}
			}
			if len(byDisk[l]) == 0 {
				whole = false // the snapshot isn't listed: at most the size the AMI records
				largest = mapGiB[l]
			}
			if keptBy[l] {
				whole = false // another AMI shares this disk's snapshots
			}
			gib += largest
			cost += largest * price
		}

		first := g.amis[0]
		res := label(aws.ToString(first.ImageId), first.Tags)
		if res == aws.ToString(first.ImageId) && aws.ToString(first.Name) != "" {
			res = fmt.Sprintf("%s (%s)", aws.ToString(first.ImageId), aws.ToString(first.Name))
		}
		key := ""
		if len(g.amis) > 1 {
			res = fmt.Sprintf("%d AMIs of %s", len(g.amis), strings.TrimPrefix(lins[0], "snap:"))
			key = "amis-of-" + lins[0]
		}
		created := parseTime(aws.ToString(first.CreationDate))
		f := Finding{
			Evidence: []string{
				fmt.Sprintf("EC2 DescribeImages: %d AMI(s) owned by this account (%s), not launched in %d days", len(g.amis), listSome(ids, 5), o.SnapshotAgeDays),
				"EC2 DescribeInstances: no non-terminated instance runs from them",
			},
			Check: "unused-ami", Region: r.Name, Resource: res, Key: key,
			Facts: facts("images", strings.Join(ids, " "), "snapshots", strings.Join(snapIDs, " "), "state", string(first.State)),
			Fix:   []string{"# shared with other accounts? They can't launch an AMI once it's gone: check each one's launch permissions first"},
		}
		if len(g.amis) == 1 {
			f.Detail = fmt.Sprintf("AMI created %s that nothing launches; its %d snapshot(s) hold %.0f GB", ageText(o.Now, created), len(snapIDs), gib)
		} else {
			f.Detail = fmt.Sprintf("%d AMIs (%s) that nothing launches, made from the same disk(s); their snapshots hold %.0f GB", len(g.amis), listSome(ids, 5), gib)
		}
		// Something costhound couldn't see may still name these AMIs: then it
		// doesn't guess. The finding stays, unpriced, with the deletes as comments.
		unsure := ""
		switch {
		case r.ImageRefs == nil:
			unsure = "launch templates, SSM parameters and Image Builder recipes couldn't be checked for references to it"
			f.Evidence = append(f.Evidence, "EC2 DescribeImageReferences: not available, so launch templates, SSM parameters and Image Builder recipes weren't checked")
		case r.LaunchConfigASGs != 0:
			unsure = "an Auto Scaling group uses a launch configuration (or they couldn't be read), which can name an AMI and can't be read without its user data"
			f.Evidence = append(f.Evidence, "EC2 DescribeImageReferences: no launch template, SSM parameter or Image Builder recipe names them")
		default:
			f.Evidence = append(f.Evidence, "EC2 DescribeImageReferences: no launch template, SSM parameter or Image Builder recipe names them")
		}
		for _, id := range ids {
			dereg := fmt.Sprintf("aws ec2 deregister-image --region %s --image-id %s --delete-associated-snapshots", r.Name, id)
			if unsure != "" {
				dereg = "# " + dereg
			}
			f.Fix = append(f.Fix, fmt.Sprintf("aws ec2 describe-image-attribute --region %s --image-id %s --attribute launchPermission", r.Name, id), dereg)
		}
		switch {
		case unsure != "":
			f.Confidence, f.Why = "low", unsure
			if gib > 0 {
				f.Detail += fmt.Sprintf("; up to %s a month, not added to the total: %s", money(cost), unsure)
			}
		case whole && gib > 0:
			f.MonthlyUSD, f.Priced = cost, true
		case gib > 0:
			f.Detail += fmt.Sprintf("; up to %s a month: other snapshots of the same disk stay, or another AMI shares one, so deleting these frees only what nothing else uses", money(cost))
		}
		out = append(out, f)
	}
	return out
}

// policyImage reports AMIs that AWS Backup or Data Lifecycle Manager made:
// their own retention policy removes them, and deleting them by hand fights it.
func policyImage(img ec2types.Image) bool {
	if strings.HasPrefix(aws.ToString(img.Name), "AwsBackup_") || strings.Contains(aws.ToString(img.Description), "AWS Backup") {
		return true
	}
	for _, k := range []string{"aws:backup:source-resource", "aws:dlm:lifecycle-policy-id"} {
		if _, ok := tag(img.Tags, k); ok {
			return true
		}
	}
	return false
}

func parseTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// IdleDedicatedHosts finds Dedicated Hosts with no instances on them: a host
// is billed by the hour from allocation to release, whatever runs on it.
func IdleDedicatedHosts(r *inventory.Region, o Options) []Finding {
	for _, s := range r.Skipped {
		if s.What == "ec2:DescribeInstances" {
			return nil // a stopped instance may sit on the host: don't guess
		}
	}
	// DescribeHosts lists only running instances; a stopped instance with host
	// affinity (a BYOL license, say) still belongs on its host.
	placed := map[string]int{}
	for _, in := range r.Instances {
		if in.Placement != nil && (in.State == nil || in.State.Name != ec2types.InstanceStateNameTerminated) {
			placed[aws.ToString(in.Placement.HostId)]++
		}
	}
	var out []Finding
	for _, h := range r.Hosts {
		if h.State != "available" || h.Instances > 0 || placed[h.ID] > 0 || h.Outpost != "" {
			continue
		}
		what := h.Family
		if h.Type != "" {
			what = h.Type
		}
		f := Finding{
			Evidence: []string{
				fmt.Sprintf("EC2 DescribeHosts: state %s, 0 instances, allocated %s", h.State, ageText(o.Now, h.Allocated)),
				"EC2 DescribeInstances: no instance, running or stopped, is placed on it",
			},
			Check: "idle-dedicated-host", Region: r.Name, Resource: h.ID,
			Facts: facts("host", h.ID, "family", h.Family, "az", h.AZ, "instances", "0", "reservation", h.Reservation),
			Detail: fmt.Sprintf("%s Dedicated Host in %s with no instances on it, allocated %s: billed by the hour whether or not anything runs",
				orDash(what), h.AZ, ageText(o.Now, h.Allocated)),
			Fix: []string{fmt.Sprintf("aws ec2 release-hosts --region %s --host-ids %s", r.Name, h.ID)},
		}
		if strings.HasPrefix(h.Family, "mac") {
			f.Fix = append([]string{"# Mac hosts can be released 24 hours after allocation at the earliest"}, f.Fix...)
		}
		if d := days(o.Now, h.Allocated); d >= 0 && d < 1 {
			f.Confidence, f.Why = "low", "allocated less than a day ago; instances may be about to land on it"
		}
		hourly, known := pricing.DedicatedHostHour(h.Family)
		switch {
		case h.Reservation != "":
			f.Detail += fmt.Sprintf("; host reservation %s pays for it, so releasing it saves nothing until the reservation ends", h.Reservation)
		case known:
			f.MonthlyUSD, f.Priced = hourly*pricing.HoursPerMonth, true
			f.Detail += fmt.Sprintf(" ($%.3f/h, the us-east-1 On-Demand list price; most other regions cost more)", hourly)
		}
		out = append(out, f)
	}
	return out
}

// DisabledKMSKeys finds customer managed KMS keys that can't be used (disabled,
// or waiting for key material that was never imported or has expired) but
// are still billed: $1/month per key version. Keys scheduled for deletion are
// free, so deletion is the way out, after checking nothing still needs them.
func DisabledKMSKeys(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, k := range r.Keys {
		why := ""
		switch k.State {
		case "Disabled":
			why = "disabled"
		case "PendingImport":
			why = "waiting for key material (never imported, or expired)"
		default:
			continue
		}
		versions := 1 + min(k.Rotations, 2)
		name := k.ID
		if k.Alias != "" {
			name += " (" + k.Alias + ")"
		}
		out = append(out, Finding{
			Evidence: []string{
				fmt.Sprintf("KMS DescribeKey: customer managed, state %s, created %s", k.State, ageText(o.Now, k.Created)),
				fmt.Sprintf("KMS ListKeyRotations: %d rotation(s); each of the first two adds a billed key version", k.Rotations),
			},
			Check: "disabled-kms-key", Region: r.Name, Resource: name, Key: k.ID,
			Facts: facts("key", k.ID, "key_arn", k.ARN, "state", k.State, "versions", itoa(versions)),
			Detail: fmt.Sprintf("customer managed key %s: it can't encrypt or decrypt, but its %d key version(s) are billed %s a month until it's scheduled for deletion (the 7-30 day wait is free)",
				why, versions, money(float64(versions)*pricing.KMSKeyVersionMonthly)),
			MonthlyUSD: float64(versions) * pricing.KMSKeyVersionMonthly, Priced: true,
			Fix: []string{
				"# data encrypted with this key can never be decrypted once it's deleted: check what still tries to use it",
				fmt.Sprintf("aws cloudtrail lookup-events --region %s --lookup-attributes AttributeKey=ResourceName,AttributeValue=%s --max-results 20", r.Name, k.ARN),
				fmt.Sprintf("aws kms schedule-key-deletion --region %s --key-id %s --pending-window-in-days 30", r.Name, k.ID),
				fmt.Sprintf("# changed your mind during the 30-day wait: aws kms cancel-key-deletion --region %s --key-id %s", r.Name, k.ID),
			},
		})
	}
	return out
}

// RDSOrphanSnapshots groups manual DB and DB cluster snapshots of databases
// that no longer exist (by name and by resource ID, so a renamed database
// still counts as live). Copies from another region or account are usually
// disaster-recovery copies of a live database: they're shown, unpriced, at
// low confidence, with every delete as a comment. DB-instance snapshots may
// be covered by the free backup allowance of the instances still running;
// snapshots of deleted clusters are always billed (Aurora's allowance is per
// cluster).
func RDSOrphanSnapshots(r *inventory.Region, o Options) []Finding {
	type group struct {
		source, engine, from string
		cluster, copy        bool
		old                  []inventory.DBSnapshot
		kept                 int
		maxGiB               int32
		covered              bool
		first, last          *time.Time
	}
	groups := map[string]*group{}
	var order []string
	for _, s := range r.DBSnapshots {
		if s.SourceExists {
			continue
		}
		key := "db:" + s.Source
		if s.Cluster {
			key = "cluster:" + s.Source
		}
		if s.Copy {
			key = "copy-" + key
		}
		g, ok := groups[key]
		if !ok {
			g = &group{source: s.Source, cluster: s.Cluster, copy: s.Copy, engine: s.Engine, from: s.CopyFrom}
			groups[key] = g
			order = append(order, key)
		}
		if d := days(o.Now, s.Created); d < o.SnapshotAgeDays {
			g.kept++
			continue
		}
		g.old = append(g.old, s)
		g.maxGiB = max(g.maxGiB, s.GiB)
		g.covered = g.covered || (!s.Cluster && s.FreeAllowance)
		if g.first == nil || (s.Created != nil && s.Created.Before(*g.first)) {
			g.first = s.Created
		}
		if g.last == nil || (s.Created != nil && s.Created.After(*g.last)) {
			g.last = s.Created
		}
	}
	var out []Finding
	for _, key := range order {
		g := groups[key]
		if len(g.old) == 0 {
			continue
		}
		sort.Slice(g.old, func(i, j int) bool { return aws.ToTime(g.old[i].Created).Before(aws.ToTime(g.old[j].Created)) })
		kind, del := "database", "delete-db-snapshot --db-snapshot-identifier"
		if g.cluster {
			kind, del = "cluster", "delete-db-cluster-snapshot --db-cluster-snapshot-identifier"
		}
		rate := pricing.RDSSnapshotGBMonth // also Multi-AZ DB clusters (MySQL, PostgreSQL)
		if g.cluster && clusterRate(g.engine) {
			rate = pricing.ClusterSnapshotGBMonth
		}
		cost := float64(g.maxGiB) * rate
		f := Finding{
			Evidence: []string{
				fmt.Sprintf("RDS DescribeDB%sSnapshots (manual): %d snapshot(s) of %s, up to %d GiB allocated", map[bool]string{true: "Cluster", false: ""}[g.cluster], len(g.old), g.source, g.maxGiB),
				fmt.Sprintf("RDS DescribeDB%ss: no %s named %s, or with its resource ID, any more", map[bool]string{true: "Cluster", false: "Instance"}[g.cluster], kind, g.source),
			},
			Check: "rds-orphan-snapshot", Region: r.Name, Key: key, Resource: fmt.Sprintf("%d snapshot(s) of %s (deleted)", len(g.old), g.source),
			Facts: facts("source", g.source, "snapshots", snapshotIDs(g.old)), // the names the text shows
			Detail: fmt.Sprintf("%s %s %s doesn't exist here; manual snapshots from %s to %s are kept until you delete them",
				orDash(g.engine), kind, g.source, dateOf(g.first), dateOf(g.last)),
		}
		switch {
		case g.copy:
			from := "another region or account"
			if g.from != "" {
				from = g.from
			}
			f.Resource = fmt.Sprintf("%d snapshot copies of %s", len(g.old), g.source)
			f.Confidence, f.Why = "low", "copies from another region or account are usually disaster-recovery or backup copies of a database that lives on"
			f.Detail = fmt.Sprintf("%d manual snapshot copies (from %s) of %s %s %s, which doesn't run in this region: usually a disaster-recovery copy kept on purpose; up to %s a month",
				len(g.old), from, orDash(g.engine), kind, g.source, money(cost))
		case g.kept > 0:
			f.Detail += fmt.Sprintf("; %d newer snapshot(s) of it are kept, so deleting these frees only what they don't share", g.kept)
		case g.covered:
			f.Detail += fmt.Sprintf("; up to %s a month (%d GiB at the us-east-1 rate of $%.3f/GB-month): the free backup allowance of the DB instances you still run here may cover part of it", money(cost), g.maxGiB, rate)
			f.Confidence, f.Why = "low", "the database is gone, but final snapshots are often kept on purpose and the region's free backup allowance may cover them"
		default:
			f.MonthlyUSD, f.Priced = cost, true
			f.Detail += fmt.Sprintf(" (%d GiB at the us-east-1 rate of $%.3f/GB-month; snapshots store only used blocks, so this is the most deleting them all frees)", g.maxGiB, rate)
		}
		var ids []string
		f.Fix = []string{"# the newest one stays commented out: keep it, or export it to S3, if the data may be needed again"}
		if g.copy {
			f.Fix = []string{"# only if this isn't your disaster-recovery or backup copy:"}
		}
		for i, s := range g.old {
			ids = append(ids, s.ID)
			line := fmt.Sprintf("aws rds %s %s --region %s", del, s.ID, r.Name)
			if g.copy || i == len(g.old)-1 {
				line = "# " + line
			}
			f.Fix = append(f.Fix, line)
		}
		f.Facts = facts("source", g.source, "kind", map[bool]string{true: "cluster", false: "db"}[g.cluster], "snapshots", strings.Join(ids, " "),
			"copy", fmt.Sprint(g.copy))
		out = append(out, f)
	}
	return out
}

// clusterRate reports cluster engines billed at the Aurora backup rate
// (Aurora, DocumentDB, Neptune); Multi-AZ DB clusters pay the RDS rate.
func clusterRate(engine string) bool {
	e := strings.ToLower(engine)
	return strings.HasPrefix(e, "aurora") || e == "docdb" || e == "neptune"
}

// eksTags are the instance tags that put an EC2 node in an EKS cluster:
// self-managed and Karpenter nodes, managed node groups, and Auto Mode.
var eksTags = []string{"eks:cluster-name", "aws:eks:cluster-name", "eks:eks-cluster-name"}

// EmptyEKSClusters finds EKS clusters with nothing that can run pods: no
// managed node groups, no Fargate profiles, no Auto Scaling group tagged as
// its nodes (even one scaled to zero), no EKS Capabilities, no hybrid nodes,
// and no running or stopped instance in its VPC that could be an untagged
// node. Auto Mode clusters are left out: EC2 hides their instances from
// DescribeInstances. The control plane bills by the hour anyway.
func EmptyEKSClusters(r *inventory.Region, o Options) []Finding {
	for _, s := range r.Skipped {
		if s.What == "ec2:DescribeInstances" || s.What == "autoscaling:DescribeAutoScalingGroups" {
			return nil // nodes are found through these: don't guess
		}
	}
	nodes, inVPC := map[string]int{}, map[string]int{}
	for _, in := range r.Instances {
		if in.State != nil && in.State.Name == ec2types.InstanceStateNameTerminated {
			continue
		}
		inVPC[aws.ToString(in.VpcId)]++
		for _, t := range in.Tags {
			k, v := aws.ToString(t.Key), aws.ToString(t.Value)
			if name, ok := strings.CutPrefix(k, "kubernetes.io/cluster/"); ok {
				nodes[name]++
				continue
			}
			for _, want := range eksTags {
				if k == want {
					nodes[v]++
				}
			}
		}
	}
	var out []Finding
	for _, c := range r.EKSClusters {
		if c.Status != "ACTIVE" || c.AutoMode || c.Hybrid || c.NodeGroups > 0 || c.Fargate > 0 || c.Capabilities > 0 ||
			r.EKSAutoScaling[c.Name] > 0 || nodes[c.Name] > 0 || c.VpcID == "" || inVPC[c.VpcID] > 0 {
			continue
		}
		hourly, known := pricing.EKSControlPlaneHour(c.Tier)
		del := fmt.Sprintf("aws eks delete-cluster --region %s --name %s", r.Name, c.Name)
		f := Finding{
			Evidence: []string{
				fmt.Sprintf("EKS: status %s, Kubernetes %s, %d managed node groups, %d Fargate profiles, %d capabilities", c.Status, orDash(c.Version), c.NodeGroups, c.Fargate, c.Capabilities),
				"Auto Scaling: no group tagged as its nodes; EC2: no instance tagged as its node, and none in its VPC",
			},
			Check: "empty-eks-cluster", Region: r.Name, Resource: c.Name,
			Facts: facts("cluster", c.Name, "status", c.Status, "version", c.Version, "vpc", c.VpcID, "tier", c.Tier, "nodegroups", "0", "fargate_profiles", "0"),
			Detail: fmt.Sprintf("EKS cluster created %s with nothing to run pods on (no node groups, no Fargate, no nodes in its VPC): the control plane is billed anyway",
				ageText(o.Now, c.Created)),
			Fix: []string{"# deleting a cluster removes everything stored in Kubernetes (namespaces, secrets, add-on settings): check nothing deploys to it"},
		}
		if known {
			f.MonthlyUSD, f.Priced = hourly*pricing.HoursPerMonth, true
			f.Detail += fmt.Sprintf(" ($%.2f/h for the %s control plane, the us-east-1 list price)", hourly, c.Tier)
		}
		switch d := days(o.Now, c.Created); {
		case c.DeletionProtection:
			f.Confidence, f.Why = "low", "deletion protection is on: someone protected it on purpose"
			f.Detail += "; deletion protection is on, so someone meant to keep it"
			f.Fix = append(f.Fix, fmt.Sprintf("# aws eks update-cluster-config --region %s --name %s --no-deletion-protection", r.Name, c.Name))
			del = "# " + del
		case d >= 0 && d < 7:
			f.Confidence, f.Why = "low", "created less than a week ago: it may not be set up yet"
			del = "# " + del
		}
		f.Fix = append(f.Fix, del)
		out = append(out, f)
	}
	return out
}

// natRouted reports whether some route uses the NAT gateway: by its ID, its
// network interface, or its private address as a next hop (VPC Route Server).
func natRouted(r *inventory.Region, n ec2types.NatGateway) bool {
	if r.NATRouted["nat:"+aws.ToString(n.NatGatewayId)] {
		return true
	}
	for _, a := range n.NatGatewayAddresses {
		if r.NATRouted["eni:"+aws.ToString(a.NetworkInterfaceId)] || r.NATRouted["ip:"+aws.ToString(n.VpcId)+"/"+aws.ToString(a.PrivateIp)] {
			return true
		}
	}
	return false
}

// natAppliance reports a Network Firewall proxy (or another appliance)
// attached to the NAT gateway: apps reach it by hostname through its VPC
// endpoint, so no route needs to target the NAT.
func natAppliance(n ec2types.NatGateway) bool {
	for _, a := range n.AttachedAppliances {
		if a.AttachmentState != ec2types.NatGatewayApplianceStateDetached {
			return true
		}
	}
	return false
}

// NATNoRoutes finds NAT gateways that nothing can send traffic through: no
// route targets them (by ID, network interface or next-hop address) and no
// appliance such as a Network Firewall proxy is attached. Each bills by the
// hour. On paid plans a NAT that still moved traffic is left alone, and
// idle-nat leaves these NATs to this check, so nothing is counted twice.
func NATNoRoutes(r *inventory.Region, o Options) []Finding {
	waived := natWaived(r, o)
	var out []Finding
	for _, n := range r.NatGateways {
		id := aws.ToString(n.NatGatewayId)
		if !natNoRoutesReports(r, n, o) {
			continue
		}
		del := fmt.Sprintf("aws ec2 delete-nat-gateway --region %s --nat-gateway-id %s", r.Name, id)
		f := Finding{
			Evidence: []string{"EC2 DescribeRouteTables: no route targets it, its network interface or its address; no appliance attached", "created " + ageText(o.Now, n.CreateTime)},
			Check:    "nat-no-routes", Region: r.Name, Resource: label(id, n.Tags),
			// window_days and max_bytes: what a fix script re-checks traffic over, as for idle-nat.
			Facts: facts("nat", id, "vpc", aws.ToString(n.VpcId), "subnet", aws.ToString(n.SubnetId),
				"window_days", itoa(max(o.UsageDays, 1)), "max_bytes", fmt.Sprintf("%.0f", o.IdleNATGiB*bytesPerGiB)),
			Detail: fmt.Sprintf("NAT gateway in %s that no route table points at: nothing can send traffic through it, but it bills by the hour",
				aws.ToString(n.VpcId)),
			MonthlyUSD: pb(r).NATGatewayMonthly(), Priced: true,
		}
		if d := days(o.Now, n.CreateTime); d >= 0 && d < 1 {
			f.Confidence, f.Why = "low", "created less than a day ago: its routes may be about to be added"
			del = "# " + del
		}
		if waived[id] {
			waiveNAT(&f)
		}
		f.Fix = []string{del,
			"# its Elastic IP stays allocated (and billed) after the delete: release it if nothing else needs that address",
			fmt.Sprintf("aws ec2 describe-nat-gateways --region %s --nat-gateway-ids %s --query 'NatGateways[0].NatGatewayAddresses[].AllocationId'", r.Name, id)}
		out = append(out, f)
	}
	return out
}

// natNoRoutesReports is nat-no-routes' test: available, nothing routes to it,
// no appliance, and (on paid plans) it didn't move IdleNATGiB anyway.
func natNoRoutesReports(r *inventory.Region, n ec2types.NatGateway, o Options) bool {
	if r.NATRouted == nil || n.State != ec2types.NatGatewayStateAvailable || natRouted(r, n) || natAppliance(n) {
		return false // unknown routes: don't guess
	}
	if natMovedTraffic(r, n, o) {
		return false // traffic got through somehow: not this check's call
	}
	return true
}

// waiveNAT turns a NAT finding into what deleting it saves while the firewall
// stays: nothing.
func waiveNAT(f *Finding) {
	f.MonthlyUSD, f.Priced = 0, false
	f.Evidence = append(f.Evidence, "Network Firewall endpoints in its VPC: AWS waives one NAT gateway hour for each endpoint hour")
	f.Detail += "; a Network Firewall endpoint in its VPC waives its hourly charge, so deleting it saves nothing while that firewall stays, apart from its Elastic IP once you release it"
}

// lbMayRefill says why a load balancer with nothing behind it may only be
// between scale-ups, so its delete stays a comment: Auto Scaling groups that
// register instances with it (also returned, for Facts), Kubernetes tags, or
// a read that failed. "" when there's no such sign.
func lbMayRefill(r *inventory.Region, keys []string, k8s string, tagsUnknown bool) (asgs []string, why, gone string) {
	seen := map[string]bool{}
	for _, k := range keys {
		for _, a := range r.ASGLoadBalancers[k] {
			if !seen[a] {
				seen[a] = true
				asgs = append(asgs, a)
			}
		}
	}
	sort.Strings(asgs)
	switch {
	case len(asgs) > 0:
		why = fmt.Sprintf("Auto Scaling group(s) %s register instances with it: it may just be scaled to 0 for now", strings.Join(asgs, ", "))
	case k8s != "" && eksGone(r, k8s, false):
		gone = k8s // its cluster is gone: nothing registers targets any more
	case k8s != "":
		who := "Kubernetes"
		if k8s != "?" {
			who = "Kubernetes cluster " + k8s
		}
		why = who + " made it (its tags say so): its targets come back with the service's nodes or pods"
	case r.ASGUnknown:
		why = "the Auto Scaling groups couldn't be read: one may be scaled to 0 and register instances with it again"
	case tagsUnknown:
		why = "its tags couldn't be read: Kubernetes may have made it, and its targets would come back with its pods"
	}
	return asgs, why, gone
}

// IdleClassicLBs finds Classic Load Balancers with no instance in service:
// they bill by the hour but can't serve anything.
func IdleClassicLBs(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, lb := range r.ClassicLBs {
		if lb.InService != 0 { // in service, or unknown (-1)
			continue
		}
		del := fmt.Sprintf("aws elb delete-load-balancer --region %s --load-balancer-name %s", r.Name, lb.Name)
		f := Finding{
			Evidence: []string{"ELB DescribeLoadBalancers: no registered instances", "created " + ageText(o.Now, lb.Created)},
			Check:    "idle-classic-lb", Region: r.Name, Resource: lb.Name,
			Detail:     "Classic Load Balancer with no registered instances, so it can't serve anything; check DNS records pointing at it before deleting",
			MonthlyUSD: pb(r).LoadBalancerMonthly("classic"), Priced: true,
			Facts: facts("lb_name", lb.Name, "dns_name", lb.DNS, "instances", itoa(lb.Instances)),
		}
		if lb.Instances > 0 {
			f.Evidence[0] = fmt.Sprintf("ELB DescribeInstanceHealth: none of its %d registered instance(s) in service", lb.Instances)
			f.Detail = fmt.Sprintf("Classic Load Balancer whose %d registered instance(s) are all out of service, so it can only answer with errors; check DNS records pointing at it before deleting", lb.Instances)
			f.Confidence, f.Why = "medium", "every instance is out of service right now: a deploy or a stopped instance looks the same, so look again later"
			del = "# " + del
		}
		if d := days(o.Now, lb.Created); d >= 0 && d < 1 {
			f.Confidence, f.Why = "low", "created less than a day ago: its instances may be about to be registered"
		}
		asgs, refill, gone := lbMayRefill(r, []string{"clb:" + lb.Name}, lb.Kubernetes, lb.TagsUnknown)
		if len(asgs) > 0 { // facts are never empty: no "asgs" means none
			f.Facts["asgs"] = strings.Join(asgs, " ")
		}
		if k := lb.Kubernetes; k != "" && k != "?" { // named in the Why: -redact hides what facts hold
			f.Facts["kubernetes_cluster"] = k
		}
		if refill != "" {
			f.Confidence, f.Why = "low", refill
		}
		if gone != "" {
			f.Evidence = append(f.Evidence, "tagged for Kubernetes cluster "+gone+"; EKS ListClusters: no cluster of that name in "+r.Name)
			f.Why = "left behind by Kubernetes cluster " + gone + ", which isn't in this region's EKS any more: delete the load balancer itself (no Service is left to delete)"
		}
		if f.Confidence != "" && !strings.HasPrefix(del, "#") {
			del = "# " + del
		}
		f.Fix = []string{"# its DNS name goes with it: check Route 53 aliases and CNAMEs that point at " + orDash(lb.DNS) + " first", del}
		out = append(out, f)
	}
	return out
}

// RDSOldClass finds RDS databases on classes AWS lists as previous
// generation, priced against the current class of the same size.
func RDSOldClass(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, d := range r.DBInstances {
		if d.Status != "available" || !pricing.RDSPreviousGeneration(d.Class) {
			continue
		}
		if rdsLooksIdle(r, d, o) { // idle-rds's now: parked, not fixed
			r.Unjudged = append(r.Unjudged, "rds-old-class|"+d.ID)
			continue
		}
		if memberOfIdleDBCluster(r, d, o) { // idle-db-cluster's now: parked, not fixed
			r.Unjudged = append(r.Unjudged, "rds-old-class|"+d.ID)
			continue
		}
		twin, arm := pricing.RDSTwin(d.Class, d.Engine, d.Version)
		f := Finding{
			Evidence: []string{fmt.Sprintf("RDS DescribeDBInstances: %s %s on %s, which AWS lists as previous generation", d.Engine, d.Version, d.Class)},
			Check:    "rds-old-class", Region: r.Name, Resource: d.ID,
			Facts: facts("db_instance", d.ID, "class", d.Class, "engine", d.Engine, "version", d.Version, "twin", twin, "multi_az", strconv.FormatBool(d.MultiAZ),
				"arn", d.ARN, "resource_id", d.ResourceID), // the IDs the hub may name it by (DropCovered)
		}
		if twin == "" {
			f.Detail = fmt.Sprintf("%s DB instance on %s (previous generation); no current class has that size, so pick one by vCPUs and memory", d.Engine, d.Class)
			f.Fix = []string{"# a class change restarts the database: pick a current class by vCPUs and memory, test it, and apply it in a maintenance window"}
			out = append(out, f)
			continue
		}
		kind := "x86"
		var why []string
		if arm {
			kind = "Graviton"
			why = append(why, "the twin is Graviton (arm64): RDS runs the same engine version on it, but test the workload first")
		}
		if strings.HasPrefix(d.Class, "db.t") {
			why = append(why, fmt.Sprintf("RDS runs T3 and T4g classes in Unlimited mode: CPU above the baseline costs $%.3f per vCPU-hour (us-east-1, open-source engines), so a busy t2 can cost more after the move", pricing.RDSUnlimitedVCPUHour))
		}
		f.Detail = fmt.Sprintf("%s DB instance on %s (previous generation); %s is the current %s class of the same size", d.Engine, d.Class, twin, kind)
		oldH, ok1 := pricing.RDSHour(d.Engine, d.Class, d.MultiAZ)
		newH, ok2 := pricing.RDSHour(d.Engine, twin, d.MultiAZ)
		switch {
		case ok1 && ok2 && oldH > newH:
			f.MonthlyUSD, f.Priced = (oldH-newH)*pricing.HoursPerMonth, true
			f.Detail += fmt.Sprintf(": $%.3f instead of $%.3f an hour (us-east-1 list prices)", newH, oldH)
		case ok1 && ok2 && oldH == newH:
			f.Detail += ": the same list price, on newer hardware"
		case ok1 && ok2:
			f.Detail += fmt.Sprintf(": $%.3f instead of $%.3f an hour, more than now (us-east-1 list prices): newer hardware, not a saving", newH, oldH)
			f.Confidence = "low"
			why = append([]string{"the current class of that size costs more than this one"}, why...)
		default:
			f.Detail += " (no list price built in for this engine)"
		}
		f.Why = strings.Join(why, "; ")
		f.Fix = []string{
			"# a class change restarts the database: test the new class, then let it apply in the next maintenance window",
			fmt.Sprintf("# aws rds modify-db-instance --region %s --db-instance-identifier %s --db-instance-class %s", r.Name, d.ID, twin),
		}
		out = append(out, f)
	}
	return out
}

func snapshotIDs(ss []inventory.DBSnapshot) string {
	ids := make([]string, 0, len(ss))
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, " ")
}

// RDSGraviton finds RDS databases on a current x86 class whose engine version
// runs on Graviton, priced against the Graviton class of the same size and
// generation. RDS runs the same engine on it: no build to change, but the
// class change restarts the database. Cluster members (Aurora) are left out,
// and an idle database is left to idle-rds.
func RDSGraviton(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, d := range r.DBInstances {
		if d.Status != "available" || d.Cluster != "" {
			continue
		}
		if rdsLooksIdle(r, d, o) { // idle-rds's now: parked, not fixed
			r.Unjudged = append(r.Unjudged, "rds-graviton|"+d.ID)
			continue
		}
		twin := pricing.RDSGravitonTwin(d.Class, d.Engine, d.Version)
		if twin == "" {
			continue
		}
		oldH, ok1 := pricing.RDSHour(d.Engine, d.Class, d.MultiAZ)
		newH, ok2 := pricing.RDSHour(d.Engine, twin, d.MultiAZ)
		if !ok1 || !ok2 || newH >= oldH {
			continue
		}
		why := ""
		if strings.HasPrefix(d.Class, "db.m6i.") || strings.HasPrefix(d.Class, "db.r6i.") {
			why = fmt.Sprintf("%s is Graviton2, a generation older than %s: check the workload's speed on it (the Graviton3 class saves less)",
				twin, strings.Join(strings.Split(d.Class, ".")[:2], "."))
		}
		out = append(out, Finding{
			Why:      why,
			Evidence: []string{fmt.Sprintf("RDS DescribeDBInstances: %s %s on %s (x86); that version runs on %s", d.Engine, d.Version, d.Class, twin)},
			Check:    "rds-graviton", Region: r.Name, Resource: d.ID,
			Detail: fmt.Sprintf("%s DB instance on %s: the Graviton class %s of the same size costs $%.3f instead of $%.3f an hour (us-east-1 list prices)",
				d.Engine, d.Class, twin, newH, oldH),
			MonthlyUSD: (oldH - newH) * pricing.HoursPerMonth, Priced: true,
			Facts: facts("db_instance", d.ID, "class", d.Class, "engine", d.Engine, "version", d.Version, "twin", twin, "multi_az", strconv.FormatBool(d.MultiAZ),
				"arn", d.ARN, "resource_id", d.ResourceID),
			Fix: []string{
				"# a class change restarts the database: test the Graviton class, then let it apply in the next maintenance window",
				fmt.Sprintf("# aws rds modify-db-instance --region %s --db-instance-identifier %s --db-instance-class %s", r.Name, d.ID, twin),
			},
		})
	}
	return out
}
