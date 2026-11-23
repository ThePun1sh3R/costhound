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

// gp3 limits (EBS docs, general-purpose.html, read 2026-11-20): up to
// 80,000 IOPS at 500 IOPS per GiB, and 2,000 MiB/s at 0.25 MiB/s per IOPS;
// on Outposts 16,000 IOPS and 1,000 MiB/s.
const (
	gp3MaxIOPS, gp3MaxThroughput               = 80000, 2000
	gp3OutpostMaxIOPS, gp3OutpostMaxThroughput = 16000, 1000
)

// ioThroughput is the most an io1/io2 volume can deliver (EBS docs,
// provisioned-iops.html): io1 0.25 MiB/s per IOPS up to 500 MiB/s while at
// most 32,000 IOPS, then 16 KiB per IOPS (1,000 MiB/s at 64,000); io2 Block
// Express 0.256 MiB/s per IOPS up to 4,000 MiB/s.
func ioThroughput(typ string, iops int32) int32 {
	if typ == "io2" {
		return min(4000, iops*256/1000)
	}
	if iops <= 32000 {
		return min(500, iops/4)
	}
	return iops / 64
}

// PIOPSToGP3 finds io1/io2 volumes whose IOPS fit gp3 (up to 80,000 IOPS, or
// 16,000 on Outposts, and 500 IOPS per GiB). gp3 is priced with the
// throughput the io1/io2 volume can deliver, up to gp3's ceiling, so the
// saving is a floor. Multi-Attach volumes are left out: AWS changes their
// type only between io1 and io2 (io1-to-io2). Unattached volumes and volumes
// of long-stopped instances are left to their own checks so nothing is
// counted twice.
func PIOPSToGP3(r *inventory.Region, o Options) []Finding {
	skip, idle := stoppedInstanceVolumes(r, o), idleEBS(r, o)
	var out []Finding
	for _, v := range r.Volumes {
		id := aws.ToString(v.VolumeId)
		typ := string(v.VolumeType)
		if (typ != "io1" && typ != "io2") || v.State != ec2types.VolumeStateInUse || skip[id] != "" || aws.ToBool(v.MultiAttachEnabled) {
			continue
		}
		if _, ok := idle[id]; ok {
			continue // reported in full by idle-attached-ebs
		}
		maxIOPS, maxThr := int32(gp3MaxIOPS), int32(gp3MaxThroughput)
		if aws.ToString(v.OutpostArn) != "" {
			maxIOPS, maxThr = gp3OutpostMaxIOPS, gp3OutpostMaxThroughput
		}
		size, iops := aws.ToInt32(v.Size), aws.ToInt32(v.Iops)
		if iops > maxIOPS || iops > 500*size {
			continue
		}
		gIOPS := max(iops, pricing.GP3FreeIOPS)
		can := ioThroughput(typ, iops)
		gThr := min(maxThr, gIOPS/4, max(pricing.GP3FreeThroughput, can))
		now := pb(r).EBSMonthly(typ, size, iops, 0)
		then := pb(r).EBSMonthly("gp3", size, gIOPS, gThr)
		if now-then < 1 {
			continue
		}
		cmd := fmt.Sprintf("aws ec2 modify-volume --region %s --volume-id %s --volume-type gp3 --iops %d --throughput %d", r.Name, id, gIOPS, gThr)
		f := Finding{
			Check: "piops-to-gp3", Region: r.Name, Resource: label(id, v.Tags),
			Detail: fmt.Sprintf("%s, %d GiB, %d IOPS: %s a month now; gp3 with %d IOPS and %d MiB/s is %s a month. Keep %s only if it needs %s",
				typ, size, iops, money(now), gIOPS, gThr, money(then), typ, map[bool]string{true: "sub-millisecond latency or 99.999% durability", false: "its IOPS delivered 99.9% of the time (gp3: 99%)"}[typ == "io2"]),
			MonthlyUSD: now - then, Priced: true,
			Fix: []string{cmd},
			Facts: facts("volume", id, "from_type", typ, "from_iops", itoa(iops), "from_throughput", itoa(can), "size_gib", itoa(size),
				"to_iops", itoa(gIOPS), "to_throughput", itoa(gThr)),
		}
		if can > gThr {
			f.Confidence = "low"
			f.Detail += fmt.Sprintf("; gp3 tops out at %d MiB/s here, and this %s volume can deliver up to %d MiB/s", gThr, typ, can)
		}
		if iops > 16000 {
			f.Confidence = "low"
			f.Why = "above 16,000 IOPS it's likely a latency-sensitive database: io2 Block Express averages under 500 microseconds, gp3 single-digit milliseconds"
		}
		// Like the fix bundle, the report's own command asks first: as a
		// comment, after the reasons, when gp3 trades latency or throughput.
		var asks []string
		if iops > 16000 {
			asks = append(asks, fmt.Sprintf("it has %d IOPS, and above 16,000 it's likely a database that relies on %s's steady performance", iops, typ))
		}
		if can > gThr {
			asks = append(asks, fmt.Sprintf("gp3 gets %d MiB/s here, and it can deliver %d MiB/s", gThr, can))
		}
		if len(asks) > 0 {
			f.Fix = []string{"# check the workload first: " + strings.Join(asks, "; and ") + ". AWS can't cancel the change once it starts", "# " + cmd}
		}
		if io2 := pb(r).EBSMonthly("io2", size, iops, 0); typ == "io1" && now-io2 >= 1 {
			f.Detail += fmt.Sprintf("; or io2, as fast or faster: %s a month (%s less), changed online", money(io2), money(now-io2))
		}
		out = append(out, f)
	}
	return out
}

// IO1ToIO2 finds Multi-Attach io1 volumes that cost less as io2. gp3 can't
// take them (AWS changes a Multi-Attach volume's type only between io1 and
// io2), and io2 costs the same per GiB but less per IOPS above 32,000; the
// change is online, even while attached, and io2 is designed for 99.999%
// durability. Other io1 volumes get the io2 price in piops-to-gp3. The EBS
// docs don't say io2 is offered on Outposts, so Outposts volumes are left
// out.
func IO1ToIO2(r *inventory.Region, o Options) []Finding {
	skip := stoppedInstanceVolumes(r, o)
	var out []Finding
	for _, v := range r.Volumes {
		id := aws.ToString(v.VolumeId)
		if v.VolumeType != ec2types.VolumeTypeIo1 || !aws.ToBool(v.MultiAttachEnabled) || aws.ToString(v.OutpostArn) != "" ||
			v.State != ec2types.VolumeStateInUse || skip[id] != "" {
			continue
		}
		size, iops := aws.ToInt32(v.Size), aws.ToInt32(v.Iops)
		now, then := pb(r).EBSMonthly("io1", size, iops, 0), pb(r).EBSMonthly("io2", size, iops, 0)
		if now-then < 1 {
			continue
		}
		out = append(out, Finding{
			Check: "io1-to-io2", Region: r.Name, Resource: label(id, v.Tags),
			Detail: fmt.Sprintf("Multi-Attach io1, %d GiB, %d IOPS: %s a month now; io2 with the same IOPS is %s a month (the same price per GiB, less per IOPS above 32,000). AWS changes it online, even while attached",
				size, iops, money(now), money(then)),
			MonthlyUSD: now - then, Priced: true,
			Fix:   []string{fmt.Sprintf("aws ec2 modify-volume --region %s --volume-id %s --volume-type io2 --iops %d", r.Name, id, iops)},
			Facts: facts("volume", id, "from_type", "io1", "from_iops", itoa(iops), "size_gib", itoa(size), "to_type", "io2", "to_iops", itoa(iops)),
		})
	}
	return out
}

// UnusedSecrets finds Secrets Manager secrets nobody has read in
// SecretDays days (each costs $0.40/mo). Secrets managed by another service
// (RDS, Redshift, ...) are left alone.
func UnusedSecrets(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, s := range r.Secrets {
		if aws.ToString(s.OwningService) != "" || days(o.Now, s.CreatedDate) < o.SecretDays {
			continue
		}
		last, lastDay := "never read", "never"
		if s.LastAccessedDate != nil {
			if days(o.Now, s.LastAccessedDate) < o.SecretDays {
				continue
			}
			lastDay = s.LastAccessedDate.UTC().Format("2006-01-02")
			last = "last read " + lastDay
		}
		name := aws.ToString(s.Name)
		out = append(out, Finding{
			Check: "unused-secret", Region: r.Name, Resource: name,
			Detail:     fmt.Sprintf("%s; created %s", last, ageText(o.Now, s.CreatedDate)),
			MonthlyUSD: pricing.SecretMonthly, Priced: true,
			Fix: []string{
				"# deletion waits 30 days and can be cancelled with restore-secret",
				fmt.Sprintf("aws secretsmanager delete-secret --region %s --secret-id %s --recovery-window-in-days 30", r.Name, shellQuote(name)),
			},
			Facts: facts("secret", aws.ToString(s.ARN), "name", name, "last_read", lastDay),
		})
	}
	return out
}

// ECRCleanup finds ECR repositories that keep untagged images (priced by
// size) or have no lifecycle policy at all.
func ECRCleanup(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, rp := range r.Repos {
		if rp.HasLifecycle && rp.UntaggedBytes == 0 {
			continue
		}
		f := Finding{Check: "ecr-cleanup", Region: r.Name, Resource: rp.Name, Facts: facts("repository", rp.Name, "has_lifecycle", fmt.Sprint(rp.HasLifecycle))}
		var why []string
		if rp.UntaggedCount > 0 {
			gb := float64(rp.UntaggedBytes) / bytesPerGiB
			why = append(why, fmt.Sprintf("%d untagged image(s), %.1f GB", rp.UntaggedCount, gb))
			f.MonthlyUSD, f.Priced = gb*pricing.ECRStorageGBMon, gb*pricing.ECRStorageGBMon >= 0.01
		}
		if !rp.HasLifecycle {
			why = append(why, "no lifecycle policy, so old images pile up forever")
			policy := `{"rules":[{"rulePriority":1,"description":"expire untagged after 14 days","selection":{"tagStatus":"untagged","countType":"sinceImagePushed","countUnit":"days","countNumber":14},"action":{"type":"expire"}}]}`
			f.Fix = []string{fmt.Sprintf("aws ecr put-lifecycle-policy --region %s --repository-name %s --lifecycle-policy-text '%s'", r.Name, rp.Name, policy)}
		} else {
			f.Fix = []string{"# the existing lifecycle policy keeps untagged images: add a rule that expires them"}
		}
		f.Detail = strings.Join(why, "; ")
		out = append(out, f)
	}
	return out
}

// LambdaOnX86 lists functions that still run on x86_64: arm64 (Graviton) is
// about 20% cheaper per GB-second. One finding per region.
func LambdaOnX86(r *inventory.Region, o Options) []Finding {
	var names []string
	for _, fn := range r.Functions {
		arm := false
		for _, a := range fn.Architectures {
			if a == "arm64" {
				arm = true
			}
		}
		if !arm {
			names = append(names, aws.ToString(fn.FunctionName))
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return []Finding{{
		Check: "lambda-x86", Region: r.Name, Key: "lambda-on-x86", Resource: fmt.Sprintf("%d function(s) on x86_64", len(names)),
		Detail: fmt.Sprintf("%s: arm64 costs about 20%% less per GB-second, usually with the same or better speed", listSome(names, 5)),
		Facts:  facts("functions", strings.Join(names, " ")), // every name the text shows (redaction collects them)
		Fix:    []string{"# code without native x86 binaries (most Python, Node.js, Java) runs unchanged: redeploy with --architectures arm64 and test"},
	}}
}

// S3NoAbortMPU lists buckets with no rule to abort incomplete multipart
// uploads: the parts of failed uploads are billed as storage forever and
// don't show up in a normal object listing. One finding per region.
func S3NoAbortMPU(r *inventory.Region, o Options) []Finding {
	var bare, withRules []string
	for _, b := range r.Buckets {
		if b.AbortsMPU {
			continue
		}
		if b.HasLifecycle {
			withRules = append(withRules, b.Name)
		} else {
			bare = append(bare, b.Name)
		}
	}
	var out []Finding
	if len(bare) > 0 {
		sort.Strings(bare)
		fix := []string{}
		for i, b := range bare {
			if i == 3 {
				fix = append(fix, fmt.Sprintf("# ... and the same for the other %d bucket(s)", len(bare)-3))
				break
			}
			fix = append(fix, fmt.Sprintf(`aws s3api put-bucket-lifecycle-configuration --region %s --bucket %s --lifecycle-configuration '{"Rules":[{"ID":"abort-incomplete-uploads","Status":"Enabled","Filter":{},"AbortIncompleteMultipartUpload":{"DaysAfterInitiation":7}}]}'`, r.Name, b))
		}
		out = append(out, Finding{
			Check: "s3-abort-mpu", Region: r.Name, Key: "buckets-without-rules", Resource: fmt.Sprintf("%d bucket(s) with no lifecycle rules", len(bare)),
			Detail: listSome(bare, 5) + ": parts of failed multipart uploads are billed forever and don't show in normal listings",
			Fix:    fix,
			Facts:  facts("buckets", strings.Join(bare, " "), "has_rules", "false"),
		})
	}
	if len(withRules) > 0 {
		sort.Strings(withRules)
		out = append(out, Finding{
			Check: "s3-abort-mpu", Region: r.Name, Key: "buckets-rules-without-abort", Resource: fmt.Sprintf("%d bucket(s) whose rules don't abort failed uploads", len(withRules)),
			Detail: listSome(withRules, 5) + ": they have lifecycle rules, but none cleans up incomplete multipart uploads",
			Fix: []string{
				"# put-bucket-lifecycle-configuration REPLACES all rules: read them first, add an AbortIncompleteMultipartUpload rule, then put them back",
				fmt.Sprintf("aws s3api get-bucket-lifecycle-configuration --region %s --bucket %s > rules.json", r.Name, withRules[0]),
			},
			Facts: facts("buckets", strings.Join(withRules, " "), "has_rules", "true"),
		})
	}
	return out
}

// S3VersionsNoExpiry finds versioned buckets (versioning on, or suspended
// with the versions it made still there) with no lifecycle rule that expires
// noncurrent versions: every overwrite and delete keeps the old copy, billed
// forever. Unpriced: the size of old versions isn't readable through the API
// without S3 Inventory or a Storage Lens export.
func S3VersionsNoExpiry(r *inventory.Region, o Options) []Finding {
	const rule = `{"ID":"expire-old-versions","Status":"Enabled","Filter":{},"NoncurrentVersionExpiration":{"NoncurrentDays":30,"NewerNoncurrentVersions":3}}`
	var out []Finding
	for _, b := range r.Buckets {
		if (b.Versioning != "Enabled" && b.Versioning != "Suspended") || b.ExpiresNoncurrent {
			continue
		}
		f := Finding{
			Check: "s3-versions-no-expiry", Region: r.Name, Resource: b.Name,
			Evidence: []string{"S3 GetBucketVersioning: " + b.Versioning, "S3 GetBucketLifecycleConfiguration: no enabled rule expires noncurrent versions"},
			Detail:   "versioned bucket that keeps every old version forever: each overwrite or delete keeps the previous copy, billed at its storage class",
			Facts:    facts("bucket", b.Name, "versioning", strings.ToLower(b.Versioning), "has_rules", strconv.FormatBool(b.HasLifecycle)),
		}
		if b.Versioning == "Suspended" {
			f.Detail = "bucket with versioning suspended that still keeps every old version it made, with no rule to expire them"
		}
		f.Detail += " (unpriced: their size isn't readable without S3 Inventory or a Storage Lens export)"
		fix := []string{"# expiring old versions deletes them for good: this rule keeps the 3 newest old versions of each object and expires the rest 30 days after they're replaced"}
		if b.HasLifecycle {
			fix = append(fix, "# put-bucket-lifecycle-configuration REPLACES all rules: read them, add this rule, then put them all back",
				fmt.Sprintf("aws s3api get-bucket-lifecycle-configuration --region %s --bucket %s > %s-rules.json", r.Name, b.Name, b.Name),
				"# rule to add: "+rule)
		} else {
			fix = append(fix, fmt.Sprintf(`# aws s3api put-bucket-lifecycle-configuration --region %s --bucket %s --lifecycle-configuration '{"Rules":[%s]}'`, r.Name, b.Name, rule))
		}
		switch {
		case b.MFADelete:
			f.Confidence, f.Why = "low", "MFA Delete is on, and S3 doesn't support lifecycle rules on such a bucket"
			fix = []string{"# S3 doesn't support lifecycle rules on a bucket with MFA Delete: turning it off takes the root user's MFA device (put-bucket-versioning --mfa), or old versions are deleted by hand"}
		case b.ObjectLock == "Enabled":
			f.Confidence, f.Why = "low", "Object Lock is on: old versions may be kept on purpose, and locked ones can't be deleted before their retention ends"
		case b.ObjectLock == "?":
			f.Confidence, f.Why = "low", "its Object Lock setting couldn't be read: old versions may be kept on purpose"
		}
		f.Fix = fix
		out = append(out, f)
	}
	return out
}

// shellQuote quotes a value for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/_+=.@-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cacheGroups folds ElastiCache clusters into what users manage: replication
// groups (all their node-group members) or standalone clusters.
type cacheGroup struct {
	Name, Engine, Version, NodeType string
	Nodes                           int
	ReplicationGroup                bool
}

func cacheGroups(r *inventory.Region) []cacheGroup {
	idx := map[string]int{}
	var out []cacheGroup
	for _, c := range r.CacheClusters {
		key, rg := c.ID, false
		if c.ReplicationGroup != "" {
			key, rg = c.ReplicationGroup, true
		}
		if i, ok := idx[key]; ok {
			out[i].Nodes += c.Nodes
			continue
		}
		idx[key] = len(out)
		out = append(out, cacheGroup{Name: key, Engine: c.Engine, Version: c.Version, NodeType: c.NodeType, Nodes: c.Nodes, ReplicationGroup: rg})
	}
	return out
}

var oldCacheFamilies = map[string]bool{"t1": true, "t2": true, "m1": true, "m2": true, "m3": true, "m4": true, "r3": true, "r4": true, "c1": true}

// OldCacheNodes finds ElastiCache clusters on previous-generation node types:
// current generations (t4g, m7g, r7g, ...) are faster for about the same
// price or less (m7g costs about 1% more than m4 at the same size).
func OldCacheNodes(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, g := range cacheGroups(r) {
		fam, _, _ := strings.Cut(strings.TrimPrefix(g.NodeType, "cache."), ".")
		if !oldCacheFamilies[fam] {
			continue
		}
		out = append(out, Finding{
			Check: "old-cache-nodes", Region: r.Name, Resource: g.Name, TagRef: cacheRef(g.Name, g.ReplicationGroup),
			Detail: fmt.Sprintf("%s %s on %d x %s, a previous-generation node type: Graviton nodes (t4g, m7g, r7g) are faster for about the same price or less", g.Engine, g.Version, g.Nodes, g.NodeType),
			Fix:    []string{"# change the node type during a maintenance window; test on a copy first"},
		})
	}
	return out
}

// DynamoDBNoAutoScaling finds provisioned tables with no auto scaling: they
// pay for their peak all day. Tables the paid idle check already flags are
// left to it.
func DynamoDBNoAutoScaling(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, t := range r.Tables {
		if !t.Provisioned || t.AutoScaled {
			continue
		}
		if paidIdleTable(r, t.Name) {
			continue
		}
		cost := pricing.DynamoDBProvisionedMonthly(t.RCU, t.WCU)
		if cost < 5 {
			continue
		}
		out = append(out, Finding{
			Check: "dynamodb-no-autoscaling", Region: r.Name, Resource: t.Name,
			Detail: fmt.Sprintf("provisioned %d RCU / %d WCU (indexes included), about %s a month, with no auto scaling: it pays for its peak all day", t.RCU, t.WCU, money(cost)),
			Fix: []string{
				"# spiky or low traffic: on-demand bills only what is used (a table can switch to on-demand 4 times in 24 hours, and back to provisioned any time)",
				fmt.Sprintf("aws dynamodb update-table --region %s --table-name %s --billing-mode PAY_PER_REQUEST", r.Name, t.Name),
			},
			Facts: facts("table", t.Name, "indexes", strings.Join(t.Indexes, " ")),
		})
	}
	return out
}

// FastSnapshotRestore finds snapshots with Fast Snapshot Restore on: each one
// costs $0.75 per hour in every AZ it's enabled in ($547.50/mo), whether or
// not anything is restored. It's meant to be turned on briefly, then off.
func FastSnapshotRestore(r *inventory.Region, o Options) []Finding {
	azs := map[string][]string{}
	var order []string
	for _, f := range r.FastRestores {
		if _, ok := azs[f.SnapshotID]; !ok {
			order = append(order, f.SnapshotID)
		}
		azs[f.SnapshotID] = append(azs[f.SnapshotID], f.AZ)
	}
	var out []Finding
	for _, snap := range order {
		zones := azs[snap]
		sort.Strings(zones)
		out = append(out, Finding{
			Evidence: []string{"EC2 DescribeFastSnapshotRestores: enabled in " + strings.Join(zones, ", ")},
			Check:    "fast-snapshot-restore", Region: r.Name, Resource: snap,
			Detail:     fmt.Sprintf("Fast Snapshot Restore is on in %d AZ(s) (%s): billed $0.75 an hour per AZ whether or not you restore anything", len(zones), strings.Join(zones, ", ")),
			MonthlyUSD: float64(len(zones)) * pricing.FastSnapshotRestoreHour * pricing.HoursPerMonth, Priced: true,
			Fix:   []string{fmt.Sprintf("aws ec2 disable-fast-snapshot-restores --region %s --source-snapshot-ids %s --availability-zones %s", r.Name, snap, strings.Join(zones, " "))},
			Facts: facts("snapshot", snap, "azs", strings.Join(zones, " ")),
		})
	}
	return out
}

// BackupOrphans lists AWS Backup recovery points of EBS volumes and EC2
// instances that no longer exist. Keeping backups after a delete is often
// the point, so they're shown with their size and cost but not added to the
// waste total: the question is whether a retention rule should drop them.
func BackupOrphans(r *inventory.Region, o Options) []Finding {
	alive := map[string]bool{}
	for _, v := range r.Volumes {
		alive["volume/"+aws.ToString(v.VolumeId)] = true
	}
	for _, in := range r.Instances {
		if in.State != nil && in.State.Name != "terminated" {
			alive["instance/"+aws.ToString(in.InstanceId)] = true
		}
	}
	type group struct {
		vault         string
		points        int
		bytes         int64
		oldest, newst *time.Time
	}
	groups := map[string]*group{}
	var order []string
	for _, p := range r.RecoveryPoints {
		if p.ResourceType != "EBS" && p.ResourceType != "EC2" {
			continue
		}
		_, res, ok := strings.Cut(p.ResourceArn, ":"+r.Name+":")
		if !ok {
			continue
		}
		_, res, _ = strings.Cut(res, ":") // drop the account: "volume/vol-..." or "instance/i-..."
		if alive[res] {
			continue
		}
		g, ok := groups[res]
		if !ok {
			g = &group{vault: p.Vault}
			groups[res] = g
			order = append(order, res)
		}
		g.points++
		g.bytes += p.Bytes
		if g.oldest == nil || (p.Created != nil && p.Created.Before(*g.oldest)) {
			g.oldest = p.Created
		}
		if g.newst == nil || (p.Created != nil && p.Created.After(*g.newst)) {
			g.newst = p.Created
		}
	}
	var out []Finding
	for _, res := range order {
		g := groups[res]
		gb := float64(g.bytes) / bytesPerGiB
		out = append(out, Finding{
			Check: "backup-orphans", Region: r.Name, Resource: res + " (deleted)",
			Detail: fmt.Sprintf("%d recovery point(s) in vault %s, %.0f GB, from %s to %s, for a resource that no longer exists: about %s a month. Keep them if you need them; otherwise give the backup plan a retention rule",
				g.points, g.vault, gb, dateOf(g.oldest), dateOf(g.newst), money(gb*pricing.BackupEBSGBMonth)),
			Fix: []string{fmt.Sprintf("aws backup list-recovery-points-by-resource --region %s --resource-arn <arn of %s>", r.Name, res)},
		})
	}
	return out
}

func dateOf(t *time.Time) string {
	if t == nil {
		return "?"
	}
	return t.Format("2006-01-02")
}

// EFSNoMountTargets finds EFS file systems with no mount targets: nothing can
// reach them, but their storage is billed in full.
func EFSNoMountTargets(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, f := range r.FileSystems {
		if f.MountTargets > 0 {
			continue
		}
		cost := pricing.EFSMonthly(f.Standard, f.IA, f.Archive, provisionedMiBps(f))
		if cost < 0.5 {
			continue
		}
		out = append(out, efsFinding("efs-no-mount-targets", r.Name, f, cost, "no mount targets, so nothing can use it"))
	}
	return out
}

func provisionedMiBps(f inventory.FileSystem) float64 {
	if f.ThroughputMode == "provisioned" {
		return f.ProvisionedMiBps
	}
	return 0
}

func efsFinding(check, region string, f inventory.FileSystem, cost float64, why string) Finding {
	name := f.ID
	if f.Name != "" {
		name += " (" + f.Name + ")"
	}
	total := float64(f.Standard+f.IA+f.Archive) / bytesPerGiB
	return Finding{
		Check: check, Region: region, Resource: name,
		Detail:     fmt.Sprintf("%s; %.0f GB stored, %s a month", why, total, money(cost)),
		MonthlyUSD: cost, Priced: true,
		Fix: []string{
			"# back it up first if the data may be needed (AWS Backup or a copy)",
			fmt.Sprintf("aws efs delete-file-system --region %s --file-system-id %s", region, f.ID),
		},
		Facts: facts("file_system", f.ID, "mount_targets", itoa(f.MountTargets)),
	}
}
