# costhound

**Find the AWS resources you pay for and don't use. One command, read-only, every finding priced in dollars with the command that fixes it.**

```
$ costhound
costhound v0.2.0 (free) · AWS waste report
Account 123456789012 · 17 region(s) · 2026-11-19 09:02 UTC

Estimated waste: $1,148.20 / month  (about $13,778.40 / year)

By check:
  Unattached EBS volumes                            4      $612.00/mo
  io1/io2 volumes that fit gp3                      2      $391.50/mo
  Load balancers with no targets                    3       $49.28/mo
  gp2 volumes that should be gp3                   41       $96.40/mo
  ...

Findings, biggest first:

     $250.00/mo  eu-west-1      vol-0a1b2c3d4e5f (old-data)
      Unattached EBS volumes: gp2, 2500 GiB, attached to nothing; created 2025-03-02 (260 days ago)
      fix: aws ec2 create-snapshot --region eu-west-1 --volume-id vol-0a1b2c3d4e5f --description "backup before delete"
           aws ec2 delete-volume --region eu-west-1 --volume-id vol-0a1b2c3d4e5f
  ...
```

*(Example numbers. Your report shows your own account.)*

## Why

AWS bills keep growing in the same places: disks nobody detached, snapshots of servers deleted years ago, IPs left behind, log groups that never expire, databases quietly billed for "extended support". AWS's own tools spread this over several consoles, some only on paid support plans, and they don't tell you what to type. costhound puts it on one page, biggest first, with a price and a fix for each.

- **One file, nothing to install.** Linux, macOS, Windows.
- **Read-only.** It only calls `Describe`/`List`/`Get` APIs and never changes anything.
- **Local.** Your data never leaves your machine. No account, no telemetry, no SaaS with access to your AWS.

## Plans

| | Free | Pro | Team |
|---|---|---|---|
| Who | anyone | one person, up to 3 AWS accounts | a company, every account |
| Checks | 41 classic waste checks | + usage-based checks, extended support, AWS Cost Optimization Hub | + Savings Plans / RI advice |
| Report | text, JSON | + interactive HTML report, "new since last run" | + one report for the whole AWS Organization |
| Fixes | a fix command per finding | + fix bundles: each fix as a script that checks again, backs up, changes, verifies, with a rollback | + one folder per account of the Organization |
| Savings | | verified savings ledger: every fix costhound confirms, and what it has saved so far | + summed across the Organization |
| Automation | | | Slack summary, CI gate |
| Dashboard | | | `costhound serve`: waste over time, new and fixed since the last run (localhost only) |
| Source | MIT, on GitHub | official binary + license key | official binary + license key |

A license key is checked offline and is tied to the AWS accounts (or AWS Organization) it was bought for.

## What it checks

### Free

| Check | What it finds | Priced |
|---|---|---|
| Unattached EBS volumes | Disks attached to nothing, billed in full | yes |
| Stopped instances still paying for disks | Instances stopped for 7+ days whose disks are still billed | yes |
| Public IPs doing nothing | Elastic IPs attached to nothing or to a stopped instance ($3.65/mo each) | yes |
| Old snapshots nobody uses | Snapshots older than 90 days not used by any of your AMIs, grouped by source volume | yes, when the whole group can go |
| gp2 volumes that should be gp3 | gp3 matches their speed for ~20% less, online, no downtime | yes |
| io1/io2 volumes that fit gp3 | Provisioned-IOPS disks whose IOPS gp3 can deliver for much less (up to 80,000 IOPS, 16,000 on Outposts; Multi-Attach disks can only change to io1/io2) | yes |
| Multi-Attach io1 volumes that cost less as io2 | Shared io1 disks above 32,000 IOPS, which gp3 can't take: io2 costs the same per GiB and less per IOPS, changed online | yes |
| Log groups that never expire | CloudWatch log groups set to "never expire" | yes (storage) |
| Load balancers with no targets | ALB/NLB/GWLB with no registered targets | yes |
| NAT traffic to S3 without the free endpoint | VPCs whose S3 traffic goes through a NAT gateway instead of the free gateway endpoint | flagged |
| Paid S3/DynamoDB endpoints where a free one works | Interface endpoints ($7.30/mo per AZ) in VPCs with no free gateway endpoint | yes |
| Old-generation instance types | t2, m4, c4, r4 and older, with what the usual pick and the cheapest current type at least as big cost an hour (plain Linux On-Demand, offered in the instance's zone); EMR nodes are left out | flagged |
| EC2 instances that could run on Graviton | Running Linux x86 instances (c, m, r and t families, 5th to 8th generation) whose Graviton type of the same size is offered in their zone, per instance or per Auto Scaling group; everything on them needs an arm64 build | yes, at On-Demand prices (low confidence) |
| Global Accelerators that route nothing | Standard accelerators that are disabled, or have no listener or no endpoint: $18.25/mo each until deleted, enabled or not (custom routing accelerators aren't read) | yes |
| Secrets nobody reads | Secrets Manager secrets not read in 90 days ($0.40/mo each) | yes |
| Container images piling up in ECR | Untagged images and repositories with no lifecycle policy | yes (storage) |
| Lambda functions on x86 instead of arm64 | arm64 is about 20% cheaper per GB-second | flagged |
| Buckets that keep failed uploads forever | Buckets with no rule to abort incomplete multipart uploads | flagged |
| ElastiCache on old node types | Clusters on t2, m4, r4 and older node types; Graviton nodes cost the same or less | flagged |
| DynamoDB tables paying for their peak all day | Provisioned tables with no auto scaling (on-demand bills only what's used) | flagged, with the table's cost |
| Snapshots with Fast Snapshot Restore left on | Fast Snapshot Restore bills $0.75/hour per snapshot per AZ ($547/mo) whether you restore or not | yes |
| Backups of resources that no longer exist | AWS Backup recovery points of deleted disks and instances, with their size and cost | flagged, with the cost |
| EFS file systems nothing can reach | File systems with no mount targets, billed for every GB | yes |
| AMIs nobody launches | Your AMIs older than 90 days that no instance or launch template uses; their snapshots are billed until you deregister them | yes |
| Dedicated Hosts running no instances | Allocated hosts with nothing on them, billed by the hour anyway (an m5 host is $3,700/mo) | yes |
| KMS keys switched off but still billed | Disabled customer managed keys (and keys with no key material) still cost $1/mo per key version; keys waiting for deletion are free | yes |
| RDS snapshots of deleted databases | Manual DB and cluster snapshots older than 90 days whose database is gone ($0.095/GB-mo, Aurora $0.021) | yes, unless live databases' free backup allowance may cover them |
| EKS clusters with nothing to run on | Clusters with no node groups, Fargate profiles, Auto Scaling groups or EKS Capabilities, and no instance in their VPC: the control plane still bills $73/mo (more on a larger control-plane tier) | yes |
| NAT gateways no route uses | NAT gateways that no route table points at: nothing can use them, and each bills $32.85/mo | yes |
| Classic Load Balancers with no instance in service | Classic Load Balancers with no registered instance, or none in service, $18.25/mo each | yes |
| RDS databases on previous-generation classes | db.m1-m4, db.r3/r4 and db.t1/t2 databases, priced against the current class of the same size (Graviton when the engine version runs on it) | yes, for MySQL, MariaDB and PostgreSQL |
| Versioned buckets that keep every old version | Buckets with versioning on (or suspended) and no rule that expires old versions; MFA Delete and Object Lock buckets are marked low | no: their size needs S3 Inventory |
| Route 53 health checks nothing uses | Health checks no record set, calculated health check or AWS service uses (run once per scan, whatever the regions) | at least their optional features: the first 50 on AWS endpoints are free |
| Route 53 hosted zones with no records | Public hosted zones with only their NS and SOA records, $0.50/mo each for an account's first 25 zones and $0.10 after; private zones, zones another AWS service made, and DNSSEC-signed ones are left out | yes |
| CloudWatch alarms on resources that are gone | Single-metric alarms with no data for 30 days whose instance, volume, NAT gateway, load balancer, DB instance, function, table, stream, cache cluster, Auto Scaling group or log group no longer exists; metric math, composites and the alarms they name, and Auto Scaling's own alarms are left out | yes, $0.10/mo each ($0.30 high resolution) after the account's 10 free alarms |
| Idle DMS replication instances and Serverless replications | Provisioned instances with no tasks or only tasks stopped or failed for at least seven days, measured from each task’s stop time. Free uses built-in us-east-1 compute rates as a lower estimate. Pro checks CloudWatch CPU and uses regional instance prices, leaving out savings a Database Savings Plan may already cover. Potential free-tier instances and Serverless replications remain unpriced because their charges are uncertain | yes, for provisioned instances with a known rate outside the free tier |
| EC2 Capacity Reservations nobody uses | On-Demand Capacity Reservations holding capacity no instance uses (Pro: never used once over the usage window), billed at the On-Demand rate either way; only those this account pays for, priced above any commitment and to their end date | yes |
| EC2 detailed monitoring nobody needs | Running instances with detailed monitoring on: about 7 one-minute metrics each, billed as custom metrics (about $0.70 to $2.10 an instance-month by the account's tier); an Auto Scaling group is one finding, fixed in its launch template | yes |
| RDS databases that could run on Graviton | MySQL, MariaDB and PostgreSQL databases on db.m5/m6i/m7i, r5/r6i/r7i or t3 whose version runs on the Graviton class of the same size | yes |
| API Gateway caches no method uses | REST API stages billed for a cache ($14.60 to $2,774/mo by size) while caching is off for every method and the stage default | yes |
| CloudTrail trails delivering a second copy of management events | A trail that copies the management events another trail (often the organization trail) already delivers in a region: AWS bills every copy after the first, $2.00 per 100,000 events (unpriced) | yes |

### Pro

| Check | What it finds |
|---|---|
| NAT gateways with (almost) no traffic | NAT gateways that moved almost nothing in 14 days, still billed $32.85/mo |
| Load balancers with targets but no traffic | ALB/NLB with targets and no requests, redirects, connections or bytes in 14 days |
| Interface and Gateway Load Balancer endpoints with no traffic | Endpoints that processed nothing: interface endpoints are billed $7.30/mo per AZ, Gateway Load Balancer endpoints $7.30/mo each (their fix lists the routes that target them, as comments) |
| Log groups that ingest the most | Where your CloudWatch Logs ingestion bill comes from, and what the Infrequent Access class saves |
| Public IPv4 on resources that may not need it | Auto-assigned public IPs on EC2 and public databases, $3.65/mo each |
| EKS clusters paying for extended support | Clusters on a Kubernetes version past standard support: 6x the control-plane price |
| RDS and Aurora on engine versions past standard support | Databases billed per vCPU-hour for extended support, plus the ones about to start (dates come from AWS, not a calendar) |
| ElastiCache Redis OSS paying for extended support | Redis OSS 4/5 caches billed extra since 2026-02-01 (v6 from 2027-02-01), priced per node from AWS's Price List |
| DynamoDB tables with provisioned capacity and no traffic | Provisioned tables (and their indexes) with zero reads and writes in 14 days |
| DynamoDB indexes nobody reads | Global secondary indexes with no reads in 90 days (whatever `-usage-days` says, for monthly and quarterly jobs) while every table write is copied into them: their storage and writes, or their own provisioned capacity; a global table's index only when it's idle in every replica |
| DynamoDB tables on the costlier table class | Tables whose storage or traffic would cost less on the other class (Standard or Standard-IA), modelled from the window's CloudWatch use and the region's prices, with its free tier; global tables are left out | yes |
| Global Accelerators with no traffic | Enabled accelerators with endpoints and no new flow or byte over the window, while their endpoint health kept reporting. Not yet seen on a real idle accelerator: if AWS stops reporting endpoint health without traffic, this check stays silent rather than guess | yes |
| Lambda functions whose calls keep failing | Functions with 10% or more failed calls (100+) over the window, still failing in the last 3 days, with what the failed calls cost at most; not counted in the total (fixing the bug lowers the bill only when the failures were retries) | flagged, with the cost |
| Lambda provisioned concurrency nobody uses | Versions and aliases kept warm for calls that don't come over the usage window (none, or far fewer than allocated; callers that bypass the alias), priced at the provisioned-concurrency rate; with Application Auto Scaling, its minimum |
| Aurora Serverless v2 clusters that could pause when idle | Clusters held at a minimum above 0 ACUs while their instances sit idle for hours: at 0 an instance pauses after 5 idle minutes and isn't billed for capacity, priced per idle stretch at the capacity measured then (the writer pauses only when the whole cluster is idle; binary logging, logical replication, global databases and provisioned instances keep it awake) |
| Fargate services sized bigger than their busiest task needs | Linux Fargate services whose busiest task's CPU and memory peaks over the window fit a cheaper valid task size (25% CPU and 33% memory to spare), priced for the tasks running now; auto-scaled services and ones deployed inside the window aren't judged | yes (Fargate Spot: flagged) |
| CloudWatch custom metrics: where the monitoring bill comes from | Namespaces with the most custom metrics (Container Insights, the CloudWatch agent, your apps) and what they cost |
| EFS file systems nobody connects to | File systems with zero client connections in 14 days |
| Kinesis streams with no records | Provisioned shards ($10.95/mo each) and on-demand streams ($29.20/mo) with nothing written or read in 14 days |
| OpenSearch and Elasticsearch domains past standard support | Since 2026-11-07 the oldest versions (Elasticsearch up to 7.8, OpenSearch 1.0-1.2 and 2.3-2.9) pay extended support equal to their instance price, so their bill doubled; the rest pay per normalized instance hour. Warned 180 days ahead |
| OpenSearch domains nobody searches or writes to | Domains with zero requests, searches and indexing in 14 days, priced with their nodes and storage |
| Transfer Family servers left stopped | Stopped servers still bill for each enabled protocol. Uses regional protocol-hour rates and reports how many service-managed users and SSH public keys deletion removes |
| Route 53 Resolver endpoints with no queries | Inbound or outbound endpoints with zero queries for at least 30 days through the scan, with health telemetry on every day. Outbound includes queries through shared rules. Uses the regional ENI-hour rate per configured IP |
| OpenSearch Serverless collection groups and collections nobody uses | An empty NextGen collection group held at a minimum above 0 OCUs (AWS bills it while the group exists); a NextGen group held above 0 while its collections had no request in 14 days (at 0 it scales to zero after 10 idle minutes); Classic collections with no request in 90 days, per group and per pool outside groups (a KMS key, vector search apart), their OCUs priced only when the whole pool is idle and their storage either way, with quieter ones named as rarely used and kept; a NextGen collection with no request and no growth in its hot storage in 90 days, at that storage. Prices never count capacity that's gone since. A Bedrock knowledge base storing its vectors in a collection is named, with the order to delete in |
| SageMaker endpoints with no invocations | Real-time endpoints whose instances ran for 14 days with zero invocations (a GPU endpoint is $1,000+/mo) |
| Redshift clusters nobody connects to | Provisioned clusters with zero database connections in 14 days: pause them and the node bill stops |
| Transit Gateway attachments with no traffic | Attachments with zero bytes in or out in 14 days, $36.50/mo each |
| ElastiCache clusters nobody reads or writes | Redis OSS, Valkey and Memcached clusters (whole replication groups) with zero commands on every node in 14 days |
| MSK clusters with no messages | Kafka clusters whose brokers took in and sent out nothing in 14 days, priced with brokers and storage |
| Client VPN endpoints nobody connects to | Endpoints with no connections and no traffic in 14 days, $73/mo per associated subnet |
| RDS databases nobody connects to | DB instances with no connection in any CloudWatch sample for 14 days while running, priced from the Price List (read replicas, their sources and cluster members are left out) |
| Classic Load Balancers with no traffic | Classic Load Balancers with instances in service and no requests or bytes in 14 days, $18.25/mo each |
| Network Firewalls with no traffic | Firewalls with no traffic in 14 days, $288/mo per endpoint, less the NAT gateway hours they waive |
| EC2 instances idle every day | Running On-Demand instances under 2% average CPU, with no CPU sample above half of one vCPU (at most 10%), and 5 MB of network on every day of 14, priced by their exact billing code (unpriced in a Capacity Reservation, which keeps billing after a stop); load balancer targets (by instance or IP) are low; Spot, fleets, Auto Scaling, EKS, ECS, EMR, Batch, Beanstalk, Karpenter and service-run instances are left out |
| API Gateway caches nothing uses | REST API caches with no hits and no misses in 14 days (by API name and stage; with no requests at all, low) |
| Disks attached to running instances with no reads or writes | Data disks (not the root disk) with no read and no write on any day of 14, priced in full with the most a snapshot would cost; EMR, Multi-Attach and Outposts disks are left out, Kubernetes and Auto Scaling disks are low |
| EMR clusters that did no work | Running or waiting clusters whose YARN ran no task or job at any 5-minute check in 14 days, with no step run, and EMR's own idle signal and HDFS agreeing; priced as EC2 plus the EMR fee of their nodes. HBase, Presto (PrestoSQL) and Trino clusters are left out, notebook clusters are low |
| EKS clusters whose nodes are held by requests pods don't use | Clusters where pod requests fill most of the nodes but pods use half of that or less even in the busiest 5 minutes of 14 days (Container Insights, each node weighed by its capacity, with 90% of the window covered): what requests sized to use (+50% CPU, +25% memory) would save in nodes, at most half the fleet; low unless every node is Karpenter's, of one type and On-Demand; with enhanced Container Insights, which namespaces hold the requests |
| MemoryDB clusters nobody reads or writes | Clusters with no command of any kind on any node in 14 days, priced per node-hour by node type and engine; members of a Multi-Region cluster are low |
| Site-to-Site VPN connections with every tunnel down | Every tunnel down in every CloudWatch sample for 14 days. Unpriced: $36.50/mo standard, $7.30 on a VPN Concentrator, $438 with large tunnels, and only EC2 can tell which (that call also returns the tunnel keys, so costhound never makes it) |
| AWS Cost Optimization Hub: rightsize, idle, Graviton | AWS's own recommendations at your real prices, without the duplicates of the checks above |

### Team

| Check | What it finds |
|---|---|
| Savings Plans and Reserved Instances to buy | Commitment recommendations, shown separately: rightsize first, then commit |
| Reserved Instances and Savings Plans about to end | EC2, RDS, ElastiCache, OpenSearch and Redshift reservations and Savings Plans that end within 30 days, or ended within the last 30: not waste, but a cliff in next month's bill |
| Databases idle at the same hours every week | RDS instances and Redshift clusters with no connections in the same stretches of every week (nights, weekends) over the last four weeks, read hour by hour, and busy at other hours: what a stop-and-start schedule saves (instance hours only), with the schedule's commands. Reserved ones are flagged, not priced |

Every enabled region is scanned in parallel. Regions your account can't use are reported once, not as errors.

## Install

Download the binary for your system from the Releases page, or build the free version from source:

```
go install github.com/ThePun1sh3R/costhound@latest
```

## Run

```
costhound                          # every enabled region, default AWS credentials
costhound -profile my-account      # a named AWS profile
costhound -regions eu-west-1,us-east-1
costhound -json > waste.json       # for scripts and spreadsheets
costhound -no-fix                  # hide the fix commands
```

Pro and Team:

```
costhound -license <key>                     # or save the key in ~/.config/costhound/license
costhound -html report.html                  # interactive report: charts, filters, savings calculator, CSV
costhound plan -out fixes                    # fix bundle: reviewable scripts with a backup and a rollback each
costhound -org                               # Team: every account of your AWS Organization
costhound -slack-webhook https://hooks.slack.com/...   # Team: post a summary
costhound -fail-over 500                     # Team: exit 3 when waste is over $500/mo (for CI)
costhound serve                              # Team: local dashboard on http://127.0.0.1:8090 over your saved runs
```

| Flag | Default | Meaning |
|---|---|---|
| `-profile` | | AWS profile to use |
| `-regions` | all enabled | comma-separated regions |
| `-json` | off | JSON output |
| `-no-fix` | off | hide fix commands |
| `-min-usd` | 0.50 | hide priced findings under this many dollars a month. Orphan alarms, unused secrets and empty hosted zones count by their region's total, so 200 alarms at $0.10 show. The report says how many it hid and what they cost together, by check, and a hidden finding is never counted as fixed |
| `-snapshot-age` | 90 | age, in days, before a snapshot, AMI or RDS snapshot is reported |
| `-stopped-days` | 7 | days stopped before an instance is reported |
| `-secret-days` | 90 | days without a read before a secret is reported |
| `-min-log-gib` | 1 | never-expiring log groups this big are listed one by one; smaller ones are grouped |
| `-concurrency` | 6 | regions scanned at the same time |
| `-timeout` | 10m | stop after this long |
| `-ignore-file` | `./.costhound.yaml` | acknowledged findings (see below); `none` turns it off |
| `-license` | | Pro/Team key, or a file holding it (also `$COSTHOUND_LICENSE`) |
| `-html` | | Pro: write the interactive HTML report to this file |
| `-usage-days` | 14 | Pro: days of CloudWatch usage to judge a resource by |
| `-min-ingest-gib` | 10 | Pro: list log groups ingesting at least this much a month |
| `-no-history` | off | Pro: don't save this run or compare it with the last one |
| `-history-dir` | `~/.config/costhound/runs` | Pro: where runs are kept |
| `-org` | off | Team: scan every active account of the AWS Organization |
| `-org-role` | `CosthoundReadOnly` | Team: role assumed in each member account |
| `-org-accounts` | all | Team: only these account IDs |
| `-slack-webhook` | | Team: post a summary to this webhook (also `$COSTHOUND_SLACK_WEBHOOK`) |
| `-fail-over` | | Team: exit with code 3 when waste is at least this many $/month |
| `-fail-on-new` | off | Team: exit with code 3 when there is new priced waste since the last run |
| `-fail-on-unowned` | | Team: exit with code 3 when waste on resources with no owner tag is at least this many $/month |
| `-fail-scope` | all | Team, with `-tf-state`: the CI gates count only the waste in those state files (`terraform`) or in none of them (`outside`) |
| `-redact` | off | Pro: print a copy to share outside the account (see "Sharing a report") |
| `-redact-key` | your config folder | Pro: the key `-redact` uses, made on first use |
| `-redact-map` | | Pro: with `-redact`, also write token → original to this file |
| `-ics` | | Pro: also write the cost-cliff calendar to this file (iCalendar) |
| `-creators` | off | Pro: for findings with no owner tag, ask CloudTrail's 90-day event history who created the resource, and leave out empty hosted zones made in the last 24 hours (CreateHostedZone); needs [`iam-policy-creators.json`](iam-policy-creators.json) (see Safety) |
| `-tf-state` | | Team: a Terraform/OpenTofu state file, or a folder of them, to mark what is managed in code (repeat for more) |
| `-sign` | | Pro: also write the JSON report to this file, signed with your key, in `<file>.sig` |
| `-sign-key` | your config folder | Pro: the signing key for `-sign` (make it with `costhound keygen`) |

## Every finding has an ID, a confidence and its evidence

Each finding carries a stable ID (`ch_8f3a1c90d2`, the same in every run), a
confidence (high, medium, low) with the reason, and the facts behind it.
`costhound explain <id>` prints all of it: the price, the evidence, the exact
read-only AWS calls it came from, the fix, and a ready-to-paste
acknowledgement.

```
costhound -json > report.json
costhound explain ch_8f3a1c90d2 -from report.json
```

## Fix bundles (Pro): scripts you review, with a backup and a rollback

```
costhound plan -out fixes                        # from the newest saved run, or -from report.json
cd fixes/123456789012
./apply-all.sh --check                           # read-only: what is still waste right now
./ch_8f3a1c90d2-unattached-ebs/apply.sh          # one fix
./ch_8f3a1c90d2-unattached-ebs/rollback.sh       # undo it
```

`costhound plan` turns the findings into a folder you can read, commit, review in a pull request and run. It calls no AWS API and runs none of the scripts. Each fix gets:

- `apply.sh`: refuses credentials for any other AWS account; checks the resource again (and its CloudWatch usage, for usage-based findings) and changes nothing if it is no longer waste; backs up first where AWS allows it (a snapshot, an AMI, the saved settings); checks once more right before the change, since a backup can take hours; prints and logs each change; verifies the result. It stops and asks for a person when something says "careful": termination protection, deletion protection, a Kubernetes volume, a load balancer with redirect or fixed-response rules or a Route 53 record pointing at it, an Auto Scaling group, a replicated secret, a bucket policy that names the endpoint being replaced.
- `rollback.sh`: undoes it from what apply.sh saved. Reversible fixes go back the way they were (a disk type, a retention setting, a capacity mode, a scheduled secret deletion, a paused Redshift cluster); restorable ones are rebuilt from their backup (a disk from its snapshot, an instance from its AMI, a load balancer with its listeners and rules, a NAT gateway with the same Elastic IP and its routes, a SageMaker endpoint from its config with its instance counts, a Dedicated Host like the old one), usually with a new ID; one-way ones (deleted snapshots, expired logs and images) say so before you run them.

The bundle's README lists the fixes easiest-to-undo first, with the saving, the confidence and what to read before running each one, and every finding that got no script and why (an instance type change, an engine upgrade or a purchase is a decision, not a script). Each account folder has an `iam-policy.json` with the actions its scripts call and the ones AWS needs for them (KMS only through EC2, for backups encrypted with your own key), an `apply-all.sh`, and a `run.log` of every change and who made it. It needs bash (macOS's 3.2 works), the AWS CLI v2, and jq for the few fixes that say so.

| Flag | Default | Meaning |
|---|---|---|
| `-out` | | the new folder to write; an existing bundle is never written over (it keeps its scripts' backup state) |
| `-from` | newest saved run | a JSON report (`costhound -json > report.json`) |
| `-account` | | with saved runs of several accounts: which one |
| `-only` | all | only these finding IDs |
| `-checks` | all | only these checks |
| `-min-confidence` | low | leave out findings below this confidence |

## Who to ask: owners

Each finding names its owner: the owner tag on its resource (`owner`, `team`,
`created-by` or `contact`, in any spelling such as `Owner` or `Created_By`),
else the CloudFormation stack, Kubernetes namespace or Kubernetes cluster that made it. A disk, load balancer or control-plane log group (`/aws/eks/<cluster>/cluster`) left by a Kubernetes cluster that EKS no longer has in its region is marked as left behind, rather than "may just be scaled to zero". The report
adds up the waste per owner and starts with the unowned part: waste on
resources with no owner tag, so nobody to ask.

Tags come from the scan's own describe calls where they return them (EC2,
Secrets Manager) and from `tag:GetResources` for the rest. costhound keeps only
the owner tags and drops every other tag value as soon as it reads it. Your own
keys go first, in `.costhound.yaml`:

```yaml
owner_tags: [squad, cost-owner]
owners:                    # one name for each owner's spellings, in any case
  payments: [pay-team, payments@example.com, payments-prod]
```

Team: the Slack summary leads with the unowned waste and the biggest owners,
and `-fail-on-unowned 200` fails CI once $200/month of waste has nobody to ask.

## Coming up: the cost-cliff calendar (Pro)

The report lists every date in the next 24 months when a resource will cost more or less if nothing changes: RDS and Aurora, EKS, ElastiCache and OpenSearch versions leaving standard support, the year-3 price step of extended support, and the upgrades AWS forces when extended support ends (or right away, where extended support is turned off). On Team, it also lists each Reserved Instance and Savings Plan as it ends, when what it covers goes back to On-Demand rates, unless a renewal is queued for all of it. Each line has the monthly change for that resource, what happens if you do nothing, and where the date comes from: an AWS API, or AWS's published schedule. A running total shows how the monthly bill moves. `-json` has them all under `upcoming`, and `-ics cliffs.ics` writes them as a calendar: one all-day event per cliff with a reminder two weeks ahead, and one per month that changes the bill. Events keep their identity when AWS moves a date, so importing the file again updates them; to subscribe from a calendar app, publish the file from your CI job to a private URL. Team: the Slack summary names the cliffs of the next 30 days.

## Terraform: what's in code, what isn't (Team)

`costhound -tf-state infra/` reads Terraform or OpenTofu state you have locally: a state file, `terraform show -json` output, or a folder of them (for remote state, run `terraform state pull > prod.tfstate` first). Each finding the state manages gets its address. Its fix then says what to change in code, because a CLI change would drift and the next apply would undo it. Fix bundles leave it out, and the report splits the waste: in Terraform (fix it in code) and in none of the state files given. State holds secrets in plain text, so costhound reads only addresses and resource IDs from it, keeps nothing else, and never fetches state itself.

In CI, `-fail-scope terraform` makes `-fail-over`, `-fail-on-new` and `-fail-on-unowned` count only the waste in the state files given (a repo's own pipeline, which fixes it in its code); `-fail-scope outside` counts only the waste in none of them (what was made by hand).

## Sharing a report (Pro): -redact

`costhound -redact` prints a copy you can hand to a consultant, an auditor or a vendor. Account IDs, resource IDs and names, ARNs, addresses and owners become tokens keyed with a secret only you hold (`-redact-key`, made on first use): the same resource gets the same token in every report, and nobody without the key can tell what it was. Fix commands, facts and acknowledgement notes are left out. If any collected name or ID would still show, nothing is printed. Names shorter than 4 characters (`dev`, `web`) stay as they are: replacing words that short would garble the text. `-redact-map tokens.json` writes the token list for you to keep. The copy carries the SHA-256 of the full report, so you can later show it came from that report. Exact totals and dates can still point to the account.

### Signed reports

`costhound keygen` makes your own signing key, which stays with you, and prints its public key: give that to whoever checks your reports. `costhound -sign report.json` (Pro) writes the JSON report and `report.json.sig`. `costhound verify -key their.pub report.json` (free) says whether the report is exactly what the key's holder signed, and spells out what a signature can't prove: that the scan saw everything, or that a finding is right. The signature also carries the costhound version, the binary's SHA-256 (compare it with the release's `SHA256SUMS`) and the run ID (the report's CloudTrail command shows that run's calls). With `-redact`, the signed copy is the redacted one.

## Acknowledging findings: .costhound.yaml

Keep a `.costhound.yaml` in git for the waste you've decided to keep. Those
findings move out of the total, Slack and the CI gate, but are still listed.
When `until` passes, they come back. Rules that match nothing are reported so
the file stays clean.

```yaml
ignore:
  - id: ch_8f3a1c90d2           # one finding
    reason: kept for the yearly audit
    owner: platform-team
    until: 2027-03-01
  - check: lambda-x86           # a whole check
    reason: arm64 migration planned for Q2
  - check: unattached-ebs       # a pattern, optionally per account/region
    resource: "vol-0abc*"
    region: eu-west-1
    reason: restore drills
```

## Permissions

costhound needs read-only access. Use the minimal policy in [`iam-policy.json`](iam-policy.json) (it grants no API that exists to return object data, secret values, VPN tunnel keys, user data or log contents; its last statement only works through EC2's AMI reference check, `aws:CalledVia ec2-images.amazonaws.com`, so EC2 can look at launch templates and SSM parameters for costhound while costhound itself can't read them; it reads DNS record settings only to match Route 53 health checks, and keeps only their IDs):

```
aws iam create-policy --policy-name costhound-readonly --policy-document file://iam-policy.json
```

To keep even settings that can hold secrets away from costhound (see Safety), use [`iam-policy-strict.json`](iam-policy-strict.json) instead: the same policy without `lambda:ListFunctions`, `lambda:ListEventSourceMappings`, `elasticmapreduce:DescribeCluster`, `elasticmapreduce:ListSteps`, `ecs:DescribeServices`, `ecs:DescribeTaskDefinition`, `bedrock:GetKnowledgeBase`, `transfer:DescribeServer`, `dms:DescribeReplicationInstances`, `dms:DescribeReplicationTasks`, `dms:DescribeReplicationConfigs`, `dms:DescribeReplications` and API Gateway's stage reads. The Lambda x86, Lambda errors, API Gateway cache, idle EMR, Fargate sizing, Transfer and DMS checks then list what they couldn't read, and idle OpenSearch Serverless collections don't name their Bedrock knowledge base; everything else runs as usual.

For `-creators` (Pro), also attach [`iam-policy-creators.json`](iam-policy-creators.json). Its one permission, `cloudtrail:LookupEvents`, reads your account's audit log, so it's in neither policy above.

For an AWS Organization, deploy [`deploy/costhound-readonly-role.yaml`](deploy/costhound-readonly-role.yaml) to every account with one CloudFormation StackSet (instructions inside), then run `costhound -org` from the management account.

If a permission is missing, costhound skips only that check and lists what it couldn't read at the end of the report. Without `tag:GetResources`, findings outside EC2 and Secrets Manager just show no owner.

## Safety

- **Read-only, enforced.** costhound calls only `Describe`, `List` and `Get` APIs, and refuses anything else inside the binary before it is sent. It also refuses the reads that return secret values or data (`GetSecretValue`, `GetObject`, `GetLogEvents`, `GetParameter`, `DescribeVpnConnections` and the like), whatever your permissions allow.
- **Settings that can hold secrets are dropped on arrival.** A few describe calls the checks need also answer with settings where people sometimes paste passwords: Lambda `ListFunctions` (environment variables, and a container image's command), API Gateway `GetStages` (stage variables), EMR `DescribeCluster` (configurations) and `ListSteps` (step arguments), and on Pro plans Lambda `ListEventSourceMappings` (event filters), ECS `DescribeServices` (a Service Connect log driver's options, such as a Splunk token), `DescribeTaskDefinition` (container environment variables) and Bedrock `GetKnowledgeBase` (a vector store's connection string and database user; costhound keeps only the knowledge base's ID, name and OpenSearch Serverless collection). DMS reads can include Kerberos configuration, replication settings and failure messages. Transfer `DescribeServer` can include custom identity provider URLs and login banners. costhound drops those settings as each answer arrives: they are never stored, printed, logged or sent, and tests plant a secret in each to prove it. With `iam-policy-strict.json`, AWS refuses those calls, so nothing comes back at all.
- **CloudTrail only when you ask.** With `-creators` (Pro), costhound reads CloudTrail's event history, your account's audit log, for findings with no owner tag, to name who created each resource, and for empty hosted zones, to leave out the ones made in the last 24 hours (CreateHostedZone, zone IDs only). It keeps the creating event's name, date and caller, and drops the rest of each event as it arrives (request parameters, source IP addresses, access keys). The creator sits next to "no owner tag" and never counts as an owner. Without the flag, costhound refuses `LookupEvents` itself. Saved runs then hold people's names; `-redact` hides them.
- **Every run says what it did.** The report lists each AWS call it made (`run` in `-json`), and its run ID is in every request's user agent, so your own CloudTrail can confirm it: the text report ends with the `aws cloudtrail lookup-events` command that should print nothing.
- **The fix commands, and the Pro fix bundles, are for you to review and run.** costhound never runs them. Destructive ones come with a backup step first (a snapshot before deleting a volume, an AMI before terminating an instance, a 30-day recovery window before deleting a secret). Commands never overwrite existing lifecycle rules.
- **It only touches what's yours.** Snapshots and AMIs are read with the owner set to your own account, and anything owned by another account is dropped again on our side.
- **It knows what's in use.** It never reports snapshots behind any of your AMIs (including deprecated and disabled ones), or snapshots managed by AWS Backup or Data Lifecycle Manager.
- **Nothing leaves your machine.** No telemetry, no account, no server. The only network call outside AWS is the Slack webhook, and only when you pass one.

## How the prices are estimated

The prices are us-east-1 on-demand list prices, built into the tool. Most other regions cost the same or up to about 20% more. Cost Optimization Hub findings use AWS's own estimate at your real prices. Snapshot storage is incremental, so a group of snapshots is priced only when every snapshot of that volume can be deleted. The numbers are for ranking where your money goes, not a copy of your bill.

## Development

```
make test     # unit tests of the free and the official build
make e2e      # runs the real binary against moto (a local AWS emulator), seeded with known waste; never touches real AWS
make release  # official binaries for linux/darwin/windows + SHA256SUMS in dist/
```

Real-AWS smoke test, on an account you own. It creates tiny tagged waste for about 15 minutes (a few cents), scans it, checks every planted resource was found, then deletes it all, even if a step fails. It refuses to start if the profile isn't the account ID you pass:

```
make build && scripts/real-aws-smoke.sh <profile> <account-id>
```

## License

The free version is MIT (see [LICENSE](LICENSE)). The Pro and Team features are not open source.
