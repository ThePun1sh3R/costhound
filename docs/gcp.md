# Google Cloud setup (preview)

First-time setup for scanning Google Cloud with costhound. Pro and Team only; the report is labelled GCP (preview).

## 1. What you need

- Application Default Credentials from your own user login: `gcloud auth application-default login`
- A Pro or Team license key (`-license`, `$COSTHOUND_LICENSE`, or `<config dir>/costhound/license`)
- Pro: up to 3 slots shared across AWS account IDs, Azure subscription IDs and GCP project IDs. The key must list each project. Pro accepts only your own user ADC (`authorized_user`), not a service account.
- Team: the key must list every project (there is no org-wide GCP cover). Team can also use a service account via ADC.

## 2. Least-privilege access

Grant **Viewer** (`roles/viewer`) and **Recommender Viewer** (`roles/recommender.viewer`) on each project you will scan. Viewer includes `logging.buckets.list` (log bucket retention), `bigquery.datasets.get`, `bigquery.tables.list`, and `bigquery.tables.get` (BigQuery storage billing model), Cloud Monitoring reads for billed log retention bytes and per-log ingest volume (`logging.googleapis.com/byte_count`), and the per-project billing link (`cloudbilling.googleapis.com` projects.getBillingInfo; Project Viewer is enough). Recommender Viewer covers every project-scoped family this scan reads (Compute idle/rightsize, Cloud SQL idle and overprovisioned, custom-image idle, GKE diagnosis cost/idle, Cloud Run cost, CUD usage-commitment, Cloud Storage bucket soft-delete, unattended project utilization, BigQuery partition/cluster and capacity-commitment advice). With **Spend Based Commitment Recommender Viewer** (`roles/recommender.ucsViewer`) on the billing account, costhound also shows spend-based commitment advice (purchase advice, not counted as waste). costhound only sends GET requests to Google APIs from a fixed allowlist, and never reads data inside resources.

**Idle BigQuery reservations** (`gcp-bigquery-idle-reservation`) flags reservations older than the idle window with baseline slots and no assignments. It skips reservations whose idle slots can be borrowed by assigned reservations in the same admin project, location, and edition. Estimated savings cover pay-as-you-go baseline slots after active capacity commitments. If access is denied, grant `roles/bigquery.resourceViewer` on the reservation admin project.

**BigQuery time-travel windows** (`gcp-bigquery-time-travel-window`) flags datasets on physical storage billing whose `maxTimeTravelHours` is above 48 while `numTimeTravelPhysicalBytes` from table storage stats is non-zero. Savings assume steady churn and price the GiB that a 2-day window would not keep at the active physical list rate. Datasets already flagged by `gcp-bigquery-storage-billing` are skipped because switching to logical billing removes time-travel charges. Fixes are review-only (`bq update --dataset=true --max_time_travel_hours=48`). The ledger observes the billing model and window hours and books when the window reaches 48 hours, the dataset is deleted, or billing switches to logical.

**Log ingest** (`gcp-log-ingest`) reads Cloud Monitoring `logging.googleapis.com/byte_count` grouped by `metric.label.log` over the same 14-day UTC window as most usage checks, scaled to a 30.4-day month. It skips _Required audit logs that are not billed, prices standard ingest from the catalog free tier and paid GiB rate, and prices VPC flow, firewall and NAT logs at the vended GiB rate.

**Cloud TPUs with idle TensorCores** (`gcp-idle-tpu`) lists TPU VM nodes by zone and checks 14 full UTC days of `tpu_worker` idle duration for READY nodes. Reserved nodes are skipped, and nodes without a covered `tpu_worker` idle metric are shown as not read. On-demand v5e, v5p and v6e estimates use the topology's chip count and the regional chip-hour rate.

**Oversized Cloud SQL** (`gcp-oversized-cloudsql`) reads Cloud Monitoring `database/cpu/utilization`, `database/memory/usage`, and (for PostgreSQL) `postgresql/num_backends_by_state` or (for MySQL, when set) `network/connections` over a 30-day UTC window. It targets Enterprise `db-custom` tiers and Enterprise Plus fixed tiers one step smaller in the same series, includes regional (HA) instances, and yields to idle Cloud SQL findings on the same instance.

**Idle Cloud SQL read replicas** (`gcp-idle-cloudsql-replica`) uses the same 14-day UTC window and CPU thresholds as idle Cloud SQL. It requires covered connection counts every day from PostgreSQL backends or MySQL connections, at or below the idle Cloud SQL bar. It skips replicas that have downstream replicas and designated cross-region failover targets. Other cross-region replicas are low confidence because they may serve disaster recovery. The estimate covers the replica's compute and storage, and the ledger verifies deletion.

**Regional HA on non-production Cloud SQL** (`gcp-cloudsql-ha-nonprod`) looks for a regional primary with an `env`, `environment`, `stage` or `tier` user label set to `dev`, `development`, `test`, `testing`, `qa`, `staging`, `stage` or `sandbox`, case-insensitively. It prices the regional-to-zonal difference for the same compute and storage. Changing availability restarts the instance. The finding includes the rollback command.

**Cloud SQL Enterprise Plus without data cache** (`gcp-cloudsql-enterprise-plus-unneeded`) looks at Enterprise Plus N2 instances with the data cache off. Every Enterprise Plus N2 tier has 8 GiB per vCPU and Enterprise allows at most 6.5, so the target is the Enterprise custom tier with the same vCPUs and the most memory Enterprise allows (an 8 vCPU / 64 GiB instance becomes `db-custom-8-53248`). It reports only when the covered memory peak stays at or below half the target memory and PostgreSQL connections fit the target's default limit, the same rules as the oversized Cloud SQL check, and prices the compute difference. Review the 99.99% SLA, near-zero-downtime maintenance and longer point-in-time recovery before changing editions. Both configuration checks yield to idle and rightsizing findings on the same instance. The ledger looks for zonal availability or the Enterprise target tier, respectively.

**Idle and oversized managed instance groups** (`gcp-idle-mig`, `gcp-oversized-mig`) judge fixed-size zonal and regional MIGs as a group: members are matched by the `created-by` metadata URL, must equal `targetSize`, and use the same idle or rightsizing rules as standalone VMs. Autoscaled, multi-version, stateful, flexible-machine-type and GKE/App Engine flexible pools are skipped; MIG members never get VM-level idle or oversized findings.

**Underrequested GKE node pools** (`gcp-gke-nodepool-underrequested`) cover fixed-size pools in Standard clusters with exactly one pool because Monitoring reports these metrics at cluster scope. The check uses 14 days of CPU and memory requests and targets 70% of each node's allocatable capacity. The estimate uses configured requests rather than actual usage, includes DaemonSet requests, and prices only Compute Engine nodes. Boot disks are excluded from the estimate.

**Over-requested GKE clusters** (`gcp-gke-overrequested`) cover Standard clusters with any number of node pools. It reads the free GKE system metrics summed over the cluster in 5-minute periods (CPU and memory requests, CPU use, non-evictable memory use, node allocatable CPU and memory) and needs 90% of the window's periods. It flags a cluster whose requests fill at least 60% of the nodes on average while use stays at or under half of the requests even in the busiest 5 minutes, for CPU and memory. Requests sized to that peak, with 50% headroom for CPU and 25% for memory, need that share of today's nodes; the saving is the rest, at most half, of the node pools' on-demand bill after any `gcp-gke-nodepool-underrequested` or `gcp-spot-candidate-nonprod` saving on the same pools. It is unpriced with Spot, preemptible or GPU pools, or when the cluster's node count doesn't match its instance groups. Medium confidence needs autoscaling on every pool and one machine type, because the cluster autoscaler removes nodes by requests, not use. The evidence names the namespaces that hold the most CPU requests. Fixes are review only: vertical Pod autoscaling in recommendation mode, then lower requests in the manifests. The ledger books only when the cluster is gone.

**Spot candidates on non-production MIGs and GKE pools** (`gcp-spot-candidate-nonprod`) flags labelled non-production managed instance groups and GKE Standard node pools still on standard (on-demand) VMs. Savings are the gap between on-demand compute after Google's full-month sustained use discount and the regional Spot list rate in the price snapshot (a lower bound; Spot VMs can be reclaimed at any time). It skips idle or oversized MIG findings, underrequested node pool findings, Spot and preemptible VMs, GPU shapes, stateful MIGs, GKE-owned MIGs, and protected resources. When a cluster's only non-Spot pool is flagged, one node per zone is left unpriced for system workloads. Fixes are review-only. The ledger books when observed provisioning state becomes `spot`.

**Public IPv4 on VMs** (`gcp-public-ipv4-in-use`) checks attached external IPv4 on running Compute Engine VMs and confirmed static IPv4 on terminated VMs. It prices each standard VM address for 730 hours using the regional catalog rate ($3.65/month in us-central1). Spot and preemptible addresses stay unpriced because their $0.0025/hour SKU is missing from the snapshot. The ledger verifies the address is no longer attached or reserved in the project. An owner should review access through Cloud NAT or a load balancer before removing an address. A static address detached from its VM bills as assigned but unused ($0.01 an hour), so the finding and its fix say to release the reservation too.

**Oversized Memorystore Redis** (`gcp-oversized-memorystore-redis`) reads `stats/memory/usage`, `stats/memory/system_memory_usage_ratio`, and `stats/evicted_keys` over a 30-day UTC window for Basic tier and Standard (HA) without read replicas. It picks the cheapest priced whole-GB size at or above need (50% headroom on data and system memory, with an 11 GiB floor on Redis 6+ instances already at 11 GiB+), skips instances that evicted keys, and yields to idle Memorystore findings on the same instance.

**Idle Memorystore for Valkey** (`gcp-idle-valkey`) reads `instance/node/clients/connected_clients` summed across nodes and `instance/commandstats/total_calls_count` by command over the idle window. Cross-region secondaries are skipped. Nodes without a covered clients series leave the instance unjudged (not read).

**Idle Memorystore for Redis Cluster** (`gcp-idle-redis-cluster`) reads `cluster/node/clients/connected_clients` summed across nodes and `cluster/commandstats/total_calls_count` by command over the idle window. Cross-region secondaries are skipped. The check requires a covered client series and estimates monthly cost from every provisioned primary and replica node.

**Oversized Filestore** (`gcp-oversized-filestore`) reads `nfs/server/used_bytes`, `snapshots_used_bytes`, and NFS ops/throughput metrics (per file share) over the same 30-day sizing window as oversized VMs. It targets shrinkable tiers (Zonal, Regional, Enterprise, High Scale SSD) with a single share and no replication replicas; Basic, Standard, Premium and fixed-IOPS custom performance are skipped. Capacity need uses daily peaks plus 30% headroom; target size respects tier step and 10 TiB band rules, and performance must stay under 50% of the instance's provisioned limits when projected onto the target. Idle Filestore wins on the same instance. The ledger books when share capacity reaches the target (smaller still books; larger does not).

Three Batch H bucket checks read Cloud Monitoring `storage/total_bytes` on `gcs_bucket`, with metric label `type` set to `soft-deleted-object`, `noncurrent-object`, or `multipart-upload`, plus a parallel query for `storage_class = STANDARD`. They use the same three-day UTC window as the large Standard bucket check, never list objects, and price savings on STANDARD bytes only (`gcp-bucket-soft-delete-bytes`, `gcp-bucket-noncurrent-no-lifecycle`, `gcp-bucket-incomplete-multipart`). Bucket metadata comes from `storage.buckets.list` (soft delete policy and lifecycle rules). The verified-savings ledger books when the bucket is deleted, soft-delete retention is shortened (pro rata on retention seconds), or a qualifying lifecycle rule is added.

**Non-production bucket in a multi-region** (`gcp-multiregion-bucket-nonprod`) targets US, EU or Asia multi-region buckets whose labels match the same non-production keys as other GCP checks (`env`, `environment`, `stage`, `tier`, and values such as `dev`, `test`, `staging`, `sandbox`). It reads Standard `storage/total_bytes` over three UTC days and prices the gap between multi-region and regional Standard list rates on the matching continent (for example US → `us-central1`). Other storage classes, write replication charges and bucket relocation itself are not priced. Relocation needs Storage Intelligence, write-locks the bucket at the final sync, and gives up cross-region failover for zone-level redundancy. Fixes are review-only (`gcloud storage buckets relocate` with a dry run first). The ledger treats a fix like the large Standard bucket check: resolved only when the bucket disappears from a complete list or the finding is otherwise gone (relocation parks until deletion).

**Standard buckets with no object reads for 30 days** (`gcp-cold-standard-bucket`) targets regional or US/EU/Asia multi-region buckets at least 31 days old with Autoclass off and no lifecycle rule that blocks Autoclass. One project-level Monitor query each for `storage/total_bytes` (by storage class), `object_count`, `api/request_count` (30 days, by method), and `network/received_bytes_count`; any failed read silences findings for the project. Cold data is estimated from Standard bytes minus uploads in the window, minus objects under 128 KiB, minus noncurrent bytes already flagged by `gcp-bucket-noncurrent-no-lifecycle`. Savings use catalog Standard and Autoclass Nearline rates (regional target from a co-located multi-region nonprod finding when present), minus the Autoclass management fee; findings need at least $5/month. Fixes are review-only (`gcloud storage buckets update --enable-autoclass`). The ledger books when Autoclass is enabled or the bucket is deleted; clearing reads or labels parks without credit.

You must set a quota project (`-quota-project` or `$GOOGLE_CLOUD_QUOTA_PROJECT`) for `x-goog-user-project` on every call.

For your user:

```
gcloud projects add-iam-policy-binding <project-id> --member="user:<email>" --role="roles/viewer"
gcloud projects add-iam-policy-binding <project-id> --member="user:<email>" --role="roles/recommender.viewer"
gcloud auth application-default login
```

Then pass `-quota-project <project-id>` on every costhound command, or set `$GOOGLE_CLOUD_QUOTA_PROJECT`.

Team, service account via ADC (for example `$GOOGLE_APPLICATION_CREDENTIALS`, or an attached account on the host):

```
gcloud projects add-iam-policy-binding <project-id> --member="serviceAccount:<sa-email>" --role="roles/viewer"
gcloud projects add-iam-policy-binding <project-id> --member="serviceAccount:<sa-email>" --role="roles/recommender.viewer"
```

Repeat the bindings for every project you scan. Pass the same or another project as `-quota-project`.

## 3. Check your setup

Works on every plan, including free and no license. No findings; access only.

```
costhound gcp -check -projects <project-id>[,…] -quota-project <project-id>
costhound gcp -check -projects <project-id> -quota-project <project-id> -json
```

Exit 0 when access is ready, 1 when any line is FIX.

| Status | Meaning | Example |
|---|---|---|
| OK | Ready | `OK   credentials: ADC type authorized_user (allowed on Pro)` · `OK   quota-project: my-quota` · `OK   compute on my-proj: enabled and readable` |
| NOTE | Not blocking | `NOTE storage on my-proj: API not enabled (nothing to scan there)` · `NOTE catalog key: $COSTHOUND_GCP_CATALOG_KEY not set (snapshot prices will be used)` |
| FIX | Must fix before a scan | `FIX  credentials: Application Default Credentials not found — gcloud auth application-default login` · `FIX  project my-proj: … — grant roles/viewer on project my-proj` · `FIX  recommender on my-proj: … — grant roles/recommender.viewer on project my-proj` · `FIX  quota-project: not set — set -quota-project or $GOOGLE_CLOUD_QUOTA_PROJECT` |

On Pro, a service-account ADC is FIX. Service accounts need Team.

## 4. Run a scan

```
costhound gcp -projects <project-id>[,…] -quota-project <project-id>
costhound gcp -projects <project-id> -quota-project <project-id> -json
costhound gcp -projects <project-id> -quota-project <project-id> -html report.html
costhound gcp -projects <project-id> -quota-project <project-id> -out fixes
```

Useful flags: `-snapshot-age` (default 90), `-no-history`, `-history-dir` (default `<config dir>/costhound/runs`), `-timeout` (default 10m), `-license`.

Each complete run is compared with the last scan of the same project set, and confirmed fixes go into the verified-savings ledger. `-no-history` skips both. Owner labels use the same keys as AWS owner tags (`owner`, `team`, `created-by`, `contact`).

Spanner, Bigtable and Filestore rightsizing use a 30-day Cloud Monitoring window (same as oversized VMs): daily maxima of utilization (and for Filestore, storage bytes plus NFS ops/throughput rates), with headroom on a smaller allowed capacity. Idle Spanner, Bigtable or Filestore findings on the same instance win over oversized in one scan. Multi-region Spanner configs are reported unpriced; Bigtable node savings use each cluster's zone region.

**Oversized AlloyDB** (`gcp-oversized-alloydb`) reads Cloud Monitoring `instance/cpu/maximum_utilization`, `instance/memory/min_available_memory` and `instance/postgres/total_connections` over that same 30-day window. It targets READY N2 highmem primaries and read pools without autoscaling one step down the 128→2 ladder, prices per vCPU and GiB × billable nodes (two for regional HA primaries, `readPoolConfig.nodeCount` for read pools), and yields to idle AlloyDB on the same instance.

```
costhound savings
costhound savings -month YYYY-MM -json -html savings.html
```

Shows verified fixes for that month across AWS, Azure and GCP history on this machine. Needs Pro or Team.

## 5. Team

```
costhound gcp … -slack-webhook https://hooks.slack.com/...
costhound gcp … -fail-over 500
costhound gcp … -fail-on-new
costhound gcp … -fail-on-unowned 200
```

`$COSTHOUND_SLACK_WEBHOOK` works instead of `-slack-webhook`. A failed Slack post warns and does not change the exit code. The CI gates exit 3. `-fail-on-new` with `-no-history` or no previous run fails nothing and prints why. Unowned waste uses resources with no owner label.

```
costhound serve
```

Local dashboard on `http://127.0.0.1:8090` over saved runs (AWS, Azure and GCP). Team only; localhost only.

## 6. Fix bundles

`-out` writes `apply.sh` and `rollback.sh`. costhound never runs them. Every apply re-checks the resource live (same immutable ID, still running, unattached or unused, with the reported machine type, disk type or size and the same labels), asks first, and changes nothing if anything differs.

| Finding | apply.sh | rollback.sh | Undo |
|---|---|---|---|
| Idle running VM | stop | start the VM | reversible |
| Unattached zonal Persistent Disk (pd-standard, pd-balanced, pd-ssd) | snapshot, then delete once READY and of this disk | recreate disk from snapshot (same name, type, size, zone, labels) | restorable |
| Unused static external IPv4 (regional or global) | release | reserve a new address, same name/place/tier/labels, **different IP** | one-way |
| Idle Cloud SQL (PostgreSQL or MySQL) | stop with `--activation-policy=NEVER` | start with `--activation-policy=ALWAYS` (storage stays billed while stopped) | reversible |
| Oversized Cloud SQL (review only) | review only: tier change restarts the instance; patch by hand | — | review only |
| Oversized Filestore (review only) | shrink file-share capacity to the target GiB the finding names (online; needs free space) | grow capacity back to the previous size | reversible online |

Disks with a Cloud KMS or customer-supplied key need a decision (exit 4). Regional, Hyperdisk and pd-extreme disks stay review steps. Everything else is review-only; the bundle README says why (for example a disk kept by a stopped VM, or idle Cloud NAT).

## 7. Prices

USD list prices from Google's Cloud Billing Catalog. A dated catalog snapshot (2026-11-28) covers all 48 Compute regions. With `$COSTHOUND_GCP_CATALOG_KEY` set, live catalog prices are used for the same SKUs. A SKU Google doesn't sell in a region, and unsupported configurations, stay unpriced with the reason in the finding. A missing catalog key is a NOTE in `-check`, not a FIX.

## 8. Privacy

GET-only to an allowlisted set of Google API routes. Metadata and metrics only; never data inside resources. Findings, history and the ledger stay on your machine. Outside Google Cloud, the only optional call is a Slack webhook you pass yourself. With `$COSTHOUND_GCP_CATALOG_KEY`, live catalog GETs use that key; without it, built-in snapshot prices are used offline.

## 9. Troubleshooting

| FIX | What to do |
|---|---|
| `Application Default Credentials not found` / `gcloud auth application-default login` | Run that login; on Pro use a user ADC, not a service account |
| `grant roles/viewer on project …` | Bind Viewer on the project |
| `grant roles/recommender.viewer on project …` | Bind Recommender Viewer on the project |
| `quota-project: not set` | Pass `-quota-project` or set `$GOOGLE_CLOUD_QUOTA_PROJECT` |
| `credentials … (not allowed on Pro)` / service accounts need Team | Upgrade, or switch to `gcloud auth application-default login` |
| `project is not ACTIVE` | Use an ACTIVE project ID |
| API NOTE, not FIX | API not enabled; nothing to scan there |

See also the [Google Cloud (preview)](../README.md#google-cloud-preview) section in the README for the full check list.
