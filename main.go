// costhound finds AWS resources that cost money for nothing and prints what each
// one costs per month, with the command to fix it. It only reads your account.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/acmpca"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/globalaccelerator"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	dms "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgtschemas "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/schemas"
	"github.com/aws/aws-sdk-go-v2/service/rolesanywhere"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	"github.com/aws/smithy-go/transport/http/protocol/awsjson"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/ignore"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/owner"
	"github.com/ThePun1sh3R/costhound/internal/report"
	"github.com/ThePun1sh3R/costhound/internal/runlog"
	classicelb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func runDefault(args []string) int {
	if len(args) > 0 && args[0] == "serve" {
		return serve(args[1:])
	}
	if len(args) > 0 && args[0] == "explain" {
		return explain(args[1:], os.Stdout)
	}
	if len(args) > 0 && args[0] == "buy" {
		return buy(args[1:])
	}
	if len(args) > 0 && args[0] == "plan" {
		return planBundle(args[1:])
	}
	if len(args) > 0 && args[0] == "keygen" {
		return keygen(args[1:], os.Stdout)
	}
	if len(args) > 0 && args[0] == "verify" {
		return verify(args[1:], os.Stdout)
	}
	fs := flag.NewFlagSet("costhound", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "costhound %s: find AWS resources that cost money for nothing (read-only)\n\nUsage: costhound [flags]\n"+
			"       costhound explain <finding id>    everything costhound knows about one finding\n"+
			"       costhound buy                     a purchase link with this account's ID already filled in\n"+
			"       costhound plan -out <new folder>  Pro: fix scripts to review and run, with a backup where AWS allows one, and a rollback\n"+
			"       costhound serve                   Team: local dashboard over saved runs\n"+
			"       costhound keygen                  make the key that signs your reports (-sign)\n"+
			"       costhound verify <report.json>    check a signed report, and what its signature proves\n\nFlags:\n", version)
		fs.PrintDefaults()
	}
	profile := fs.String("profile", "", "AWS profile to use (default: the usual AWS credential chain)")
	regionsFlag := fs.String("regions", "", "comma-separated regions to scan (default: every region enabled in the account)")
	asJSON := fs.Bool("json", false, "print JSON instead of the text report")
	htmlOut := fs.String("html", "", "Pro: also write an interactive HTML report to this file")
	slackHook := fs.String("slack-webhook", os.Getenv("COSTHOUND_SLACK_WEBHOOK"), "Team: post a summary to this Slack-compatible webhook (or $COSTHOUND_SLACK_WEBHOOK)")
	failOver := fs.Float64("fail-over", 0, "Team: exit with code 3 when estimated waste is at least this many dollars a month (for CI)")
	failOnNew := fs.Bool("fail-on-new", false, "Team: exit with code 3 when there is priced waste that is new since the last run (for CI)")
	redact := fs.Bool("redact", false, "Pro: print a copy to share outside the account: IDs, names, ARNs, addresses and owners become keyed tokens; fix commands are left out")
	redactFull := fs.String("redact-full", "", "Pro: with -redact, also save the full JSON report to this new file, whose SHA-256 the redacted copy holds (keep it private)")
	redactKey := fs.String("redact-key", "", "Pro: the key -redact uses (default: costhound/redact.key in your config folder, made on first use)")
	var tfStates stringList
	creators := fs.Bool("creators", false, "Pro: for findings with no owner tag, ask CloudTrail's 90-day event history who created the resource (needs iam-policy-creators.json)")
	fs.Var(&tfStates, "tf-state", "Team: a Terraform/OpenTofu state file (raw, or terraform show -json) or a folder of them; findings get the resource that manages them (repeat for more)")
	icsPath := fs.String("ics", "", "Pro: also write the cost-cliff calendar to this file (iCalendar), to import or publish")
	signPath := fs.String("sign", "", "Pro: also write the JSON report to this file, and its signature (made with your key, see costhound keygen) to <file>.sig")
	signKey := fs.String("sign-key", "", "Pro: the signing key for -sign (default: costhound/report-signing.key in your config folder)")
	redactMap := fs.String("redact-map", "", "Pro: with -redact, also write token -> original to this file (keep it private)")
	failOnUnowned := fs.Float64("fail-on-unowned", 0, "Team: exit with code 3 when waste on resources with no owner tag is at least this many dollars a month (for CI)")
	failScope := fs.String("fail-scope", "all", "Team, with -tf-state: the CI gates count only the waste in those state files (terraform) or in none of them (outside)")
	orgMode := fs.Bool("org", false, "Team: scan every active account of the AWS Organization (run from the management account or a delegated administrator account)")
	orgRole := fs.String("org-role", "CosthoundReadOnly", "Team: role to assume in each member account (deploy it with deploy/costhound-readonly-role.yaml)")
	orgAccounts := fs.String("org-accounts", "", "Team: only these comma-separated account IDs of the Organization")
	ignoreFile := fs.String("ignore-file", "", "acknowledged findings (default: ./"+ignore.DefaultFile+" if it exists; \"none\" turns it off)")
	noHistory := fs.Bool("no-history", false, "Pro: don't save this run or compare it with the last one")
	historyDir := fs.String("history-dir", "", "Pro: where runs are saved (default: <config dir>/costhound/runs)")
	noFix := fs.Bool("no-fix", false, "hide the fix commands")
	minUSD := fs.Float64("min-usd", 0.50, "hide priced findings below this many dollars a month; alarms, secrets, hosted zones and detailed monitoring count by their region's total (the report says how many it hid, by check)")
	snapDays := fs.Int("snapshot-age", 90, "report snapshots, AMIs and RDS snapshots older than this many days")
	stoppedDays := fs.Int("stopped-days", 7, "report instances stopped for at least this many days")
	secretDays := fs.Int("secret-days", 90, "report secrets nobody has read for this many days")
	minLogGiB := fs.Float64("min-log-gib", 1, "list never-expiring log groups this big one by one (smaller ones are grouped)")
	usageDays := fs.Int("usage-days", 14, "Pro: days of CloudWatch usage to judge a resource by (and its minimum age)")
	minIngest := fs.Float64("min-ingest-gib", 10, "Pro: list log groups ingesting at least this many GiB a month")
	concurrency := fs.Int("concurrency", 6, "regions scanned at the same time")
	timeout := fs.Duration("timeout", 10*time.Minute, "give up after this long")
	licenseFlag := fs.String("license", "", "Pro/Team license key, or a file holding it (default: $"+license.EnvVar+", then <config dir>/costhound/license)")
	showVersion := fs.Bool("version", false, "print the version and plan, then exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *redactFull != "" && !*redact {
		fmt.Fprintln(os.Stderr, "costhound: -redact-full needs -redact")
		return 2
	}
	now := time.Now()
	lic := license.Resolve(*licenseFlag, license.PublicKey, now)
	if lic.Problem != "" {
		fmt.Fprintf(os.Stderr, "costhound: license from %s not accepted: %s. Running the free checks.\n", lic.From, lic.Problem)
	}
	if w := license.ExpiryWarning(lic.Claims, now); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	if lic.Plan != license.PlanFree && !checks.BuildHasPro {
		fmt.Fprintln(os.Stderr, "costhound: this build is from source and has only the free checks; the paid features are in the official release.")
	}
	if *showVersion {
		if lic.Plan != license.PlanFree {
			fmt.Printf("costhound %s (%s, license %s for %s)\n", version, lic.Plan, lic.ID, lic.Claims.Scope())
		} else {
			fmt.Printf("costhound %s (free)\n", version)
		}
		return 0
	}
	if *concurrency < 1 {
		*concurrency = 1
	}
	// Refuse a bad -redact-full path before any AWS call, so -ics/-sign/-html
	// are not written only for the full report to fail after the scan.
	if *redactFull != "" && license.Rank(lic.Plan) >= license.Rank(license.PlanPro) {
		if err := checkRedactFullPath(*redactFull); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 2
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	opts := []func(*config.LoadOptions) error{config.WithRetryMaxAttempts(8)}
	if *profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(*profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, configHelp(err))
		return 1
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	// Every client made from cfg from here on records its calls, refuses
	// writes and secret reads, and names this run in its user agent.
	run := runlog.New()
	if *creators {
		run.Allow("LookupEvents") // CloudTrail's event history: only when asked
	}
	run.Attach(&cfg)
	started := time.Now()

	who, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		fmt.Fprintln(os.Stderr, loginHelp(err))
		return 1
	}
	account := aws.ToString(who.Account)

	// A paid key only counts for the accounts (or Organization) it was sold for.
	org := ""
	if lic.Claims.Org != "" {
		if o, err := organizations.NewFromConfig(cfg).DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{}); err == nil && o.Organization != nil {
			org = aws.ToString(o.Organization.Id)
		}
	}
	if lic = lic.ForAccount(account, org); lic.Problem != "" {
		fmt.Fprintf(os.Stderr, "costhound: %s. Running the free checks.\n", lic.Problem)
	}

	now = time.Now()
	checkOpts := checks.DefaultOptions(now)
	checkOpts.SnapshotAgeDays = *snapDays
	checkOpts.StoppedDays = *stoppedDays
	checkOpts.MinLogGiB = *minLogGiB
	checkOpts.SecretDays = *secretDays
	checkOpts.Plan = lic.Plan
	checkOpts.UsageDays = *usageDays
	checkOpts.Creators = *creators && !*orgMode
	checkOpts.MinIngestGiBMonth = *minIngest
	rules, err := loadIgnore(*ignoreFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 2
	}
	if err := checkFailScope(*failScope, len(tfStates)); err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 2
	}
	st := settings{regionsFlag: *regionsFlag, o: checkOpts, concurrency: *concurrency, minUSD: *minUSD, noHistory: *noHistory, historyDir: *historyDir, ignore: rules, org: org,
		owners: owner.NewKeys(rules.OwnerKeys()).WithAliases(rules.OwnerAliases())}

	var rep *report.Report
	if *orgMode {
		if license.Rank(lic.Plan) < license.Rank(license.PlanTeam) {
			fmt.Fprintln(os.Stderr, "costhound: scanning a whole AWS Organization (-org) is part of costhound Team.")
			return 1
		}
		rep, err = scanOrganization(ctx, cfg, account, lic, *orgRole, *orgAccounts, st)
	} else {
		rep, err = scanAccount(ctx, cfg, account, st)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 1
	}
	if ctx.Err() != nil {
		fmt.Fprintf(os.Stderr, "costhound: timed out after %s; the report below is incomplete\n", *timeout)
	}
	if len(tfStates) > 0 {
		if err := mapTerraform(rep, tfStates); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	}
	// After the Terraform state, which names an owner too.
	if *creators {
		if *orgMode {
			fmt.Fprintln(os.Stderr, "costhound: -creators doesn't cover -org scans yet; skipped")
		} else {
			findCreators(ctx, cfg, rep, st.owners)
		}
	}
	// The run record last: -creators' CloudTrail calls are calls too.
	rep.Run = &report.RunInfo{ID: run.ID, UserAgent: run.UserAgent(), Caller: aws.ToString(who.Arn),
		Started: started, Finished: time.Now(), Calls: run.Calls()}
	fullReport := rep
	if *redact {
		// Only what is printed or sent is redacted: the saved history keeps the full report.
		if rep, err = redactReport(rep, *redactKey, *redactMap); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	}
	if *icsPath != "" {
		if err := writeICS(rep, *icsPath); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	}
	if *signPath != "" {
		// Signs what is printed: the redacted copy when -redact is set.
		if err := signReport(rep, *signPath, *signKey); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	}
	if *htmlOut != "" {
		if err := writeHTML(rep, *htmlOut); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	}
	if *redactFull != "" {
		if err := writeFullReport(fullReport, *redactFull); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: can't save the full report (-redact-full): %v\n", err)
			return 1
		}
	}
	if len(rep.NotOffered) > 0 {
		report.WriteNotOffered(os.Stderr, rep.NotOffered)
	}
	if *asJSON {
		if err := rep.JSON(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
	} else {
		rep.Text(os.Stdout, !*noFix)
	}
	return teamActions(ctx, rep, *slackHook, *failOver, *failOnNew, *failOnUnowned, *failScope)
}

// checkRedactFullPath refuses a -redact-full path that can't be created as a
// new file (already there, missing folder, or folder not writable). It does
// not create the path: writeFullReport's O_EXCL write is the final guard.
func checkRedactFullPath(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists: -redact-full never overwrites", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("folder %s does not exist", dir)
		}
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	tmp, err := os.CreateTemp(dir, ".costhound-redact-full-*")
	if err != nil {
		return fmt.Errorf("folder %s is not writable", dir)
	}
	name := tmp.Name()
	tmp.Close()
	os.Remove(name)
	return nil
}

// checkFailScope: -fail-scope is all, terraform or outside, and the last two
// need -tf-state to know what Terraform manages.
func checkFailScope(scope string, states int) error {
	switch scope {
	case "all":
		return nil
	case "terraform", "outside":
		if states == 0 {
			return fmt.Errorf("-fail-scope %s needs -tf-state: without state files costhound can't tell what Terraform manages", scope)
		}
		return nil
	}
	return fmt.Errorf("-fail-scope is all, terraform or outside, not %q", scope)
}

func pickRegions(ctx context.Context, cfg aws.Config, flagValue string) ([]string, error) {
	if flagValue != "" {
		var out []string
		for _, r := range strings.Split(flagValue, ",") {
			if r = strings.TrimSpace(r); r != "" {
				out = append(out, r)
			}
		}
		return out, nil
	}
	res, err := ec2.NewFromConfig(cfg).DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Regions {
		out = append(out, aws.ToString(r.RegionName))
	}
	sort.Strings(out)
	return out, nil
}

// bucketsByRegion holds S3 buckets (listed once per account) per region,
// plus the skips from listing them.
type bucketsByRegion struct {
	by    map[string][]inventory.Bucket
	skips []inventory.Skip
}

// loadBuckets lists S3 buckets once and reads each one's lifecycle rules in
// its own region; buckets in regions not being scanned are ignored.
func loadBuckets(ctx context.Context, cfg aws.Config, regions []string) bucketsByRegion {
	scanned := map[string]bool{}
	for _, r := range regions {
		scanned[r] = true
	}
	clients := map[string]*s3.Client{}
	forRegion := func(region string) inventory.S3API {
		if c, ok := clients[region]; ok {
			return c
		}
		c := cfg.Copy()
		c.Region = region
		clients[region] = s3.NewFromConfig(c)
		return clients[region]
	}
	all, skips := inventory.LoadBuckets(ctx, forRegion(cfg.Region), forRegion)
	out := bucketsByRegion{by: map[string][]inventory.Bucket{}}
	for _, b := range all {
		if scanned[b.Region] {
			out.by[b.Region] = append(out.by[b.Region], b)
		}
	}
	for _, sk := range skips {
		if sk.Region == "global" || scanned[sk.Region] {
			out.skips = append(out.skips, sk)
		}
	}
	return out
}

func scan(ctx context.Context, cfg aws.Config, account string, regions []string, o checks.Options, workers int, buckets bucketsByRegion, owners owner.Keys) ([]checks.Finding, []checks.Cliff, []inventory.Skip, map[string]string, []string, []string, []string, map[string]map[string]string, map[string]inventory.NodeGroup, map[string]inventory.Warehouse) {
	var (
		mu          sync.Mutex
		findings    []checks.Finding
		cliffs      []checks.Cliff
		skipped     []inventory.Skip
		stopped     []string                           // "|region|id": for the diff (report.Stopped)
		eksClusters []string                           // "|region|name": for the diff (report.EKSClusters)
		unjudged    []string                           // "|region|check|key": for the diff (report.Unjudged)
		asgMembers  = map[string]map[string]string{}   // region → instance ID → Auto Scaling group name
		searchNodes = map[string]inventory.NodeGroup{} // OpenSearch data-node groups, for the diff (report.OpenSearchDataNodes)
		warehouses  = map[string]inventory.Warehouse{} // Redshift clusters by ARN, to verify a resize (report.RedshiftClusters)
		standard    int                                // standard-resolution single-metric alarms, for the account's 10 free ones
		custom      int                                // custom metrics in all (paid plans), for ec2-detailed-monitoring's tier
		customOK    = true                             // every region's custom metrics were read
		done        int
		wg          sync.WaitGroup
	)
	pc := cfg.Copy()
	pc.Region = "us-east-1" // the Price List API endpoint
	priceList := pricing.NewFromConfig(pc)
	sources := map[string]string{}
	jobs := make(chan string)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for region := range jobs {
				c := cfg.Copy()
				c.Region = region
				inv := inventory.Load(ctx, region, account, inventory.Clients{
					EC2:         ec2.NewFromConfig(c),
					Logs:        cloudwatchlogs.NewFromConfig(c),
					ELB:         elb.NewFromConfig(c),
					Secrets:     secretsmanager.NewFromConfig(c),
					ECR:         ecr.NewFromConfig(c),
					Lambda:      lambda.NewFromConfig(c),
					ElastiCache: elasticache.NewFromConfig(c),
					DynamoDB:    dynamodb.NewFromConfig(c),
					AutoScaling: applicationautoscaling.NewFromConfig(c),
					FastRestore: ec2.NewFromConfig(c),
					Backup:      backup.NewFromConfig(c),
					EFS:         efs.NewFromConfig(c),
					Kinesis:     kinesis.NewFromConfig(c),

					ImageRefs:     ec2.NewFromConfig(c),
					ASG:           autoscaling.NewFromConfig(c),
					Hosts:         ec2.NewFromConfig(c),
					Offerings:     ec2.NewFromConfig(c),
					KMS:           kms.NewFromConfig(c),
					RDS:           rds.NewFromConfig(c),
					EKS:           eks.NewFromConfig(c),
					Routes:        ec2.NewFromConfig(c),
					Classic:       classicelb.NewFromConfig(c),
					APIGateway:    apigateway.NewFromConfig(c),
					CloudTrail:    cloudtrail.NewFromConfig(c),
					ELBTags:       elb.NewFromConfig(c),
					ELBRules:      elb.NewFromConfig(c),
					Alarms:        cloudwatch.NewFromConfig(c),
					Capacity:      ec2.NewFromConfig(c),
					Fleets:        ec2.NewFromConfig(c),
					SpotFleets:    ec2.NewFromConfig(c),
					Batch:         batch.NewFromConfig(c),
					DMS:           dms.NewFromConfig(c),
					PCA:           acmpca.NewFromConfig(c),
					RolesAnywhere: rolesanywhere.NewFromConfig(c),
					ACM:           acm.NewFromConfig(c),
					WorkSpaces:    workspaces.NewFromConfig(c),
				})
				inv.Buckets = buckets.by[region]
				loadPaid(ctx, c, inv, o, account, priceList) // paid plans: usage, the Price List, commitments
				f := checks.Run(inv, o)
				cl := checks.Cliffs(inv, o)
				// One owner lookup for both: the calendar's resources are the checks' own.
				all := make([]checks.Finding, 0, len(f)+len(cl))
				all = append(all, f...)
				for _, x := range cl {
					all = append(all, checks.Finding{TagRef: x.TagRef})
				}
				owner.AttachWithNativeTags(ctx, inv, all, taggingClient(c), owners, serviceOwnerTags(ctx, c, inv, all))
				f = all[:len(f)]
				if o.Creators {
					stampCreated(inv, f)
				}
				for i := range cl {
					cl[i].Owner, cl[i].OwnerFrom = all[len(f)+i].Owner, all[len(f)+i].OwnerFrom
				}
				mu.Lock()
				cliffs = append(cliffs, cl...)
				if inv.Prices != nil {
					sources[region] = inv.Prices.Source
				}
				findings = append(findings, f...)
				skipped = append(skipped, inv.Skipped...)
				standard += inv.StandardAlarms()
				if n, ok := inv.CustomMetricCount(); ok {
					custom += n
				} else {
					customOK = false
				}
				if names := inventory.InstanceASGNames(inv.Instances); len(names) > 0 {
					asgMembers[region] = names
				}
				for _, in := range inv.Instances {
					if s := in.State; s != nil && (s.Name == "stopped" || s.Name == "stopping") {
						stopped = append(stopped, "|"+region+"|"+aws.ToString(in.InstanceId))
					}
				}
				for _, name := range inv.EKSNames { // every listed cluster, even one whose details couldn't be read: it exists
					eksClusters = append(eksClusters, "|"+region+"|"+name)
				}
				recordOpenSearchDataNodes(inv, searchNodes)
				for _, u := range inv.Unjudged {
					unjudged = append(unjudged, "|"+region+"|"+u)
				}
				recordRedshiftClusters(inv, warehouses)
				done++
				fmt.Fprintf(os.Stderr, "  %-16s done (%d/%d)\n", region, done, len(regions))
				mu.Unlock()
			}
		}()
	}
	for _, r := range regions {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	checks.AlarmFreeTier(findings, standard)
	checks.MonitoringTier(findings, custom, customOK)
	var indexParked []string
	findings, indexParked = globalIndexes(findings, regions) // Pro: a global table's index, judged across its replicas
	unjudged = append(unjudged, indexParked...)
	sort.Strings(stopped)
	sort.Strings(eksClusters)
	return findings, cliffs, append(skipped, buckets.skips...), sources, stopped, unjudged, eksClusters, asgMembers, searchNodes, warehouses
}

// globalFindings runs the checks on account-wide services (Route 53) once
// per scan, whichever regions were picked; their APIs answer in us-east-1.
func globalFindings(ctx context.Context, cfg aws.Config, o checks.Options, owners owner.Keys) ([]checks.Finding, []inventory.Skip, []string, *inventory.Region) {
	c := cfg.Copy()
	c.Region = "us-east-1"
	g := inventory.LoadGlobal(ctx, inventory.GlobalClients{Route53: route53.NewFromConfig(c), Alarms: cloudwatch.NewFromConfig(c)})
	loadGlobalShield(ctx, cfg, g, o)
	loadGlobalCloudFront(ctx, cfg, g, o)
	f := youngZonesOut(ctx, c, o, g, checks.Run(g, o))
	owner.Attach(ctx, g, f, taggingClient(c), owners)
	var unjudged []string // "|global|check|key", for the diff (report.Unjudged)
	for _, u := range g.Unjudged {
		unjudged = append(unjudged, "|global|"+u)
	}
	return f, g.Skipped, unjudged, g
}

// acceleratorFindings runs the Global Accelerator checks once per scan,
// whichever regions were picked: its API, metrics and tags live in
// us-west-2. Owners come from each accelerator's own tags; the us-west-2
// tagging client is the fallback. The skips of the calls other checks share
// name us-west-2 ("cloudwatch:GetMetricData (us-west-2)", "tag:GetResources
// (us-west-2)"), so an SCP that denies that region never blocks a fix
// counted elsewhere; the globalaccelerator calls are these checks' sources
// alone. Global Accelerator is only in the aws partition: a scan of another
// partition's regions leaves it out.
func acceleratorFindings(ctx context.Context, cfg aws.Config, regions []string, o checks.Options, owners owner.Keys) ([]checks.Finding, []inventory.Skip) {
	if otherPartition(cfg.Region) || slices.ContainsFunc(regions, otherPartition) {
		return nil, nil
	}
	c := cfg.Copy()
	c.Region = "us-west-2"
	g := &inventory.Region{Name: "global"}
	accs, ok := inventory.LoadAccelerators(ctx, globalaccelerator.NewFromConfig(c), func(what string, err error) {
		g.Skipped = append(g.Skipped, inventory.Skip{Region: "global", What: what, Error: inventory.ShortError(err)})
	})
	if !ok || len(accs) == 0 {
		return nil, g.Skipped
	}
	g.Accelerators = accs
	acceleratorUsage(ctx, c, g, o)
	f := checks.Run(g, o)
	owner.Attach(ctx, g, f, taggingClient(c), owners)
	for i := range g.Skipped {
		if g.Skipped[i].What == "tag:GetResources" {
			g.Skipped[i].What = "tag:GetResources (us-west-2)"
		}
	}
	return f, g.Skipped
}

// otherPartition: a region outside the aws partition (China, GovCloud, the
// ISO regions, the European Sovereign Cloud), where Global Accelerator
// doesn't exist.
func otherPartition(region string) bool {
	for _, p := range []string{"cn-", "us-gov-", "us-iso", "eu-isoe-", "eusc-"} {
		if strings.HasPrefix(region, p) {
			return true
		}
	}
	return false
}

// taggingClient talks AWS JSON 1.1 to the Resource Groups Tagging API: the
// protocol it has always had (the AWS CLI's), rather than the CBOR the SDK
// switched to in November 2026, which the moto end-to-end test can't serve.
func taggingClient(c aws.Config) *resourcegroupstaggingapi.Client {
	return resourcegroupstaggingapi.NewFromConfig(c, func(o *resourcegroupstaggingapi.Options) {
		o.Protocol = awsjson.New11(rgtschemas.ResourceGroupsTaggingAPI_20170126)
	})
}

// settings are the scan options shared by every account of a run.
type settings struct {
	regionsFlag string
	o           checks.Options
	concurrency int
	minUSD      float64
	noHistory   bool
	historyDir  string
	ignore      *ignore.File
	org         string // the account's AWS Organization, when the license names one
	owners      owner.Keys
	orgPlans    *orgPlans // -org: every scanned account's Savings Plans
	orgHub      *orgHub   // -org: the Organization management account's Cost Optimization Hub
}

// scanAccount runs the whole pipeline for one account: regions, checks, Cost
// Optimization Hub, report, history.
func scanAccount(ctx context.Context, cfg aws.Config, account string, st settings) (*report.Report, error) {
	regions, err := pickRegions(ctx, cfg, st.regionsFlag)
	if err != nil {
		return nil, fmt.Errorf("can't list regions (%s); pass -regions to choose them yourself", inventory.ShortError(err))
	}
	fmt.Fprintf(os.Stderr, "costhound: scanning account %s in %d region(s)...\n", account, len(regions))
	findings, cliffs, skipped, priceSources, stopped, unjudged, eksClusters, asgMembers, searchNodes, warehouses := scan(ctx, cfg, account, regions, st.o, st.concurrency, loadBuckets(ctx, cfg, regions), st.owners)
	gf, gs, gu, g := globalFindings(ctx, cfg, st.o, st.owners)
	// Orphan alarms a Route 53 health check reads are left out, now that the health checks are read.
	var hcParked []string
	findings, hcParked = checks.HealthCheckAlarms(findings, g)
	findings, skipped, unjudged = append(findings, gf...), append(skipped, gs...), append(append(unjudged, gu...), hcParked...)
	af, as := acceleratorFindings(ctx, cfg, regions, st.o, st.owners)
	findings, skipped = append(findings, af...), append(skipped, as...)
	paid := license.Rank(st.o.Plan) >= license.Rank(license.PlanPro)
	var parked []string
	findings, cliffs, skipped, parked = paidAccount(ctx, cfg, st, account, regions, findings, cliffs, skipped, asgMembers) // paid plans: the hub, commitments, Savings Plans
	unjudged = append(unjudged, parked...)
	rep := report.New(version, account, st.o.Now, regions, findings, skipped, st.minUSD)
	rep.Plan = st.o.Plan
	rep.Org = st.org
	rep.OrgHub = st.orgHub.ok()
	rep.PriceNote = report.PriceNoteFor(priceSources)
	rep.Stopped = stopped
	rep.EKSClusters = eksClusters
	rep.OpenSearchDataNodes = searchNodes
	rep.Unjudged = unjudged
	rep.RedshiftClusters = warehouses
	rep.SetUpcoming(cliffs)
	rep.Settings = fmt.Sprintf("min-usd=%g snapshot-age=%d stopped-days=%d secret-days=%d min-log-gib=%g usage-days=%d min-ingest-gib=%g",
		st.minUSD, st.o.SnapshotAgeDays, st.o.StoppedDays, st.o.SecretDays, st.o.MinLogGiB, st.o.UsageDays, st.o.MinIngestGiBMonth)
	st.ignore.Apply(rep, st.o.Now)
	if paid && !st.noHistory {
		keepHistory(rep, st.historyDir, ctx.Err() == nil)
	}
	return rep, nil
}

// loadIgnore reads the acknowledgements file: the -ignore-file flag, else
// ./.costhound.yaml when it exists. "none" turns it off.
func loadIgnore(flagValue string) (*ignore.File, error) {
	switch flagValue {
	case "none":
		return nil, nil
	case "":
		return ignore.Load(ignore.DefaultFile, false)
	}
	return ignore.Load(flagValue, true)
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }
