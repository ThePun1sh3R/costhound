# Azure setup (preview)

First-time setup for scanning Azure with costhound. Pro and Team only; the report is labelled Azure (preview).

## 1. What you need

- The Azure CLI, signed in: `az login`
- A Pro or Team license key (`-license`, `$COSTHOUND_LICENSE`, or `<config dir>/costhound/license`)
- Pro: up to 3 slots shared across AWS account IDs, Azure subscription IDs and GCP project IDs. The key must list each subscription. Scans use `-auth cli` (your own interactive `az login` only).
- Team: listed subscriptions, or a whole Entra tenant on the key. Service-principal scans need Team.

## 2. Least-privilege access

Grant the built-in **Reader** role on each subscription you will scan. costhound only sends GET requests to Azure Resource Manager from a fixed allowlist, and never reads data inside resources.

For your user (after `az login`):

```
az role assignment create --assignee <your-object-id-or-upn> --role Reader --scope /subscriptions/<sub-guid>
```

Team, unattended service principal (secret in `$AZURE_CLIENT_SECRET`, never on the command line):

```
az ad sp create-for-rbac --name <app-name> --role Reader --scopes /subscriptions/<sub-guid>
```

Use the returned `appId` as `-client-id`. Repeat `--scopes` (or further `az role assignment create`) for every subscription you scan.

Reservations and savings plans are tenant-wide commitments. Subscription Reader usually does not list them. To judge underused reservations and savings plans, also assign **Reservations Reader** and **Savings plan Reader** at tenant scope (or broader). An empty list from a scan without those roles is not proof there are none.

## 3. Check your setup

Works on every plan, including free and no license. No findings; access only.

```
costhound azure -check -auth cli -tenant <tenant-guid> -subscriptions <sub-guid>[,…]
costhound azure -check -auth cli -tenant <tenant-guid> -subscriptions <sub-guid> -json
```

Exit 0 when access is ready, 1 when any line is FIX.

| Status | Meaning | Example |
|---|---|---|
| OK | Ready | `OK   credentials: mode cli (allowed on Pro)` · `OK   subscription <guid>: readable` · `OK   Retail Prices API: reachable` |
| NOTE | Not blocking | `NOTE Microsoft.Sql on <guid>: provider not registered (nothing to scan there)` |
| FIX | Must fix before a scan | `FIX  subscription <guid>: … — grant Reader on subscription <guid>` · `FIX  credentials: … — az login` · `FIX  Retail Prices API: unreachable — allow HTTPS to prices.azure.com` |

On Pro, a service-principal token (even via `-auth cli`) is FIX: use interactive `az login`. Service-principal mode itself needs Team.

## 4. Run a scan

```
costhound azure -auth cli -tenant <tenant-guid> -subscriptions <sub-guid>[,…]
costhound azure -auth cli -tenant <tenant-guid> -subscriptions <sub-guid> -json
costhound azure -auth cli -tenant <tenant-guid> -subscriptions <sub-guid> -html report.html
costhound azure -auth cli -tenant <tenant-guid> -subscriptions <sub-guid> -out fixes
```

Useful flags: `-snapshot-age` (default 90), `-no-history`, `-history-dir` (default `<config dir>/costhound/runs`), `-timeout` (default 10m), `-license`.

Each complete run is compared with the last scan of the same subscription set, and confirmed fixes go into the verified-savings ledger. `-no-history` skips both.

```
costhound savings
costhound savings -month YYYY-MM -json -html savings.html
```

Shows verified fixes for that month across AWS, Azure and GCP history on this machine. Needs Pro or Team.

## 5. Team

```
costhound azure -auth service-principal -tenant <tenant-guid> -subscriptions <sub-guid> -client-id <app-guid>
costhound azure … -slack-webhook https://hooks.slack.com/...
costhound azure … -fail-over 500
costhound azure … -fail-on-new
costhound azure … -fail-on-unowned 200
```

`$COSTHOUND_SLACK_WEBHOOK` works instead of `-slack-webhook`. A failed Slack post warns and does not change the exit code. The CI gates exit 3. `-fail-on-new` with `-no-history` or no previous run fails nothing and prints why.

```
costhound serve
```

Local dashboard on `http://127.0.0.1:8090` over saved runs (AWS, Azure and GCP). Team only; localhost only.

## 6. Fix bundles

`-out` writes `apply.sh` and `rollback.sh`. costhound never runs them. Every apply re-checks the resource live (still exists and still stopped, unattached, unused or empty, with the reported size or SKU and the same tags), asks first, and changes nothing if anything differs.

| Finding | apply.sh | rollback.sh | Undo |
|---|---|---|---|
| Stopped-but-allocated VM | deallocate | start the VM | reversible |
| Unattached managed disk | incremental snapshot, then delete | recreate disk from snapshot (same name, SKU, size, zone, tags) | restorable |
| Unused Standard public IP | delete | new Standard static IP, same name/zones/tags, **different address** | one-way |
| Empty App Service plan (Linux or Windows) | delete | recreate empty plan (same SKU, workers, OS, tags; Windows without `--is-linux`) | restorable |
| Empty Functions Premium plan (EP1–EP3) | delete | recreate empty plan (same SKU, instance count, tags) | restorable |
| Idle PostgreSQL/MySQL flexible server (not a replica, HA off) | stop | start the server | reversible |
| Idle Synapse dedicated SQL pool (workspace or standalone DataWarehouse) | pause | resume (storage stays billed while paused) | reversible |
| Idle Container Instances group | stop | start (local disk is lost; public IP may change) | reversible |
| Idle Consumption Container App with minimum replicas | set minimum replicas to zero | restore the scanned minimum | reversible |
| Unused Azure Hybrid Benefit (Windows VM, Uniform scale set, SQL VM, SQL database, elastic pool or managed instance) | re-check the billing flag and apply the benefit | restore the PAYG billing flag | reversible |
| Idle Azure OpenAI provisioned deployment (review only, not one of the ten) | print recreation settings and a commented delete command | — | review only |

Disks with a customer-managed key, Azure Disk Encryption, Trusted Launch or confidential VM security type, or private-link-only access need a decision (exit 4). Ultra Disk and Premium SSD v2 stay review steps. Everything else is review-only; the bundle README says why (for example a disk kept by a deallocated VM, an empty load balancer, or an idle NAT gateway).

Oversized provisioned Gen5 vCore SQL database findings are also review-only: online scale-down can drop connections at the switch, and Business Critical read scale-out load is not in the primary metrics.

DTU SQL database resizing is review-only. Connections can briefly drop at the end of scaling.

## 7. Prices

USD list prices from the public Azure Retail Prices API (`prices.azure.com`). A missing or ambiguous meter leaves a finding unpriced.

| Check | What it finds |
|---|---|
| `azure-idle-sql-database` | Basic, Standard and Premium single databases with low DTU usage, no sessions and no successful connections over the covered idle window. Savings use the database's regional daily compute rate. |
| `azure-idle-sql-elastic-pool` | Standard DTU elastic pools with a complete database list where every database is online, low covered pool DTU, zero pool sessions and zero successful connections on each database over the idle window. Savings use the pool's regional daily eDTU rate. |
| `azure-oversized-sql-dtu` | Standard and Premium single databases with 30 full UTC days of hourly maximum metrics. DTU, worker and session peaks must use at most 50% of the next smaller size in the same tier. Peak used space and the configured maximum must both fit in the storage included with that size. Idle findings take precedence. Savings are the difference between regional daily compute rates, scaled to a 730-hour month. |

### SQL Managed Instance

Subscription Reader covers `Microsoft.Sql/managedInstances/read`, `Microsoft.Sql/locations/instanceFailoverGroups/read`, and Monitor metrics. Both checks skip instances in pools or failover groups, passive replicas, protected instances, and instances whose failover group list could not be read.

- `azure-idle-sql-managed-instance` requires 14 full UTC days with daily maximum CPU at or below 5% and at most 1 GiB of writes across the window. Monitor has no connection metric, so confidence is low. The fix is review-only. General Purpose instances can be stopped if they have neither zone redundancy nor a Managed Instance link. The ledger verifies a stopped or deleted instance.
- `azure-oversized-sql-managed-instance` requires 30 full UTC days of CPU peaks. It suggests the next smaller Gen5 size in the same tier when projected CPU stays at or below 50% and reserved storage fits. Monitor has no memory metric, so confidence is low. Review the change with the owner because it can take hours and ends with a brief failover. The ledger verifies deletion or vCores at or below the target.

Idle findings take precedence. Estimates use 730 hours of Gen5 compute and include the SQL license meter unless the instance uses BasePrice. Non-Gen5 idle findings are unpriced. Data and backup storage stay billed.

`azure-previous-generation-vm` flags standalone running VMs on D/Ds v1–v3, A_v2, F/Fs v1–v2, and G/Gs sizes Microsoft has announced for retirement. It picks the cheapest offered D or E v5 size in the same region with at least the same vCPUs and memory, using the retail pay-as-you-go hourly delta at 730 hours. VMs with Ultra disks, ephemeral OS disks, or idle, oversized or stopped-allocated findings from the same scan are skipped. The fix is review-only. The ledger books when the VM is gone or its size is no longer the reported old size.

`azure-app-service-premium-v2` targets paid dedicated Linux or Windows App Service plans on Premium v2 (P1v2, P2v2 or P3v2). Savings are workers times the regional Premium v2 minus same-vCPU Premium v3 worker hourly rates at 730 hours. Plans already flagged idle, empty or for removal, elastic premium SKUs, ASE plans, elastic scale, or zero workers are skipped; if the same plan has an oversized-workers finding, only the remaining worker count is priced. Scale-up to Premium v3 is review-only because Premium v3 may be unavailable on the deployment unit. The ledger books when the plan is gone or its observed SKU is Premium v3 or v4.

`azure-oversized-vmss` checks 30 full UTC days of per-instance metrics on Uniform scale sets. Every instance must fit one size down in the same family with the same headroom as the VM check. The estimate uses the hourly PAYG difference for the set's OS times capacity and 730 hours. Idle sets and sets with autoscale are skipped.

`azure-idle-aks-cluster` flags running VMSS-backed clusters when hourly Azure Monitor metrics show no ready pods outside system namespaces and kube-system reports on every day of the idle window (14 full UTC days by default). The estimate covers regular and Spot node compute at retail rates and excludes the tier fee and node OS disks. A cluster finding takes precedence over Spot findings for its pools. Stopping requires review because Azure releases the capacity and a restart can fail. The ledger verifies a stopped or deleted cluster.

`azure-aks-autoscaler-min-count` flags running VMSS-backed clusters whose autoscaled node pools sit at their minimum counts while the cluster autoscaler keeps reporting unneeded nodes (preview metric `cluster_autoscaler_unneeded_nodes_count`, hourly Average over the metric window). Savings come from lowering pool minimums so the autoscaler can remove nodes it has already marked unneeded. Pools with a `azure-spot-candidate-nonprod` finding in the same scan are priced at Spot for this check so the two savings add up to the full change. Idle AKS clusters are skipped. The ledger books a share when the observed node floor drops toward `target_nodes`, or the full saving when the cluster is deleted.

`azure-aks-standard-tier-nonprod` flags running Standard Base-tier AKS clusters whose own tags mark them non-production, with one to nine running nodes across pools, and no protect or standby tags. It prices a lower bound on the Standard Uptime SLA cluster management fee minus the optional FreeTierInfrastructureCost meter when that rate exists in the region. Clusters already flagged by `azure-empty-aks-standard` or `azure-idle-aks-cluster` in the same scan are skipped. Downgrading to Free is review-only because it removes the financially backed uptime SLA. The ledger books when the cluster tier becomes Free or the cluster is deleted.

`azure-spot-candidate-nonprod` targets non-production Uniform scale sets and AKS user node pools that still run regular-priority VMs (not already Spot). Savings are capacity times the pay-as-you-go minus Spot retail hourly delta at 730 hours in the resource region. Spot list prices change and Spot VMs can be evicted with short notice, so findings are low confidence and review-only. AKS-managed scale sets, system pools, capacity reservations, dedicated hosts and protected tags are skipped; scale sets flagged by `azure-idle-vmss` or `azure-oversized-vmss` in the same scan are skipped, so the same VM hours are never priced twice. The fix is review only: a scale set's or node pool's priority is set at creation, so it means a new Spot scale set or node pool.

`azure-ml-idle-min-nodes` uses Reader access to list workspaces and computes, then checks 14 full UTC days of per-cluster metrics for dedicated nodes kept running above peak job usage.

`azure-backup-orphaned-vm` lists Recovery Services vaults, protected items and vault usages per subscription. It flags Azure IaaS VM backup items whose source VM is no longer in the subscription VM list, with a last recovery point at least 7 days before the scan. Prices the protected-instance meter at half the smallest retail tier and vault backup storage when every item in the vault is an orphan candidate and usage counts match. Reader covers `Microsoft.RecoveryServices/vaults/read`, `Microsoft.RecoveryServices/Vaults/backupProtectedItems/read`, `Microsoft.RecoveryServices/Vaults/usages/read`, and `Microsoft.Compute/virtualMachines/read`.

`azure-commitment-underused` lists the tenant's reservations and savings plans once per scan (not per subscription). A succeeded commitment older than 30 days with 30-day utilization below 95% and more than seven days until expiry is flagged; priced VM reservations use retail Reservation meters, savings plans use the hourly commitment amount. Non-VM reservations and non-USD or non-hourly savings plans stay unpriced. Findings under $5/month are omitted. Fixes are review-only (widen scope, exchange or return reservations, move compute into savings plan scope). The ledger books a shrinking unused share, a full refund or exchange, or a full save when the commitment disappears from a complete list; expiry alone books nothing. Needs `Microsoft.Capacity/reservations/read` and `Microsoft.BillingBenefits/savingsPlans/read` at tenant scope (Reservations Reader and Savings plan Reader; subscription Reader is not enough).

`azure-idle-managed-hsm` flags pools awaiting activation for at least 7 days or with zero service API hits on each of 14 full UTC days. Azure charges the full hourly rate through soft deletion until purge. `azure-deleted-managed-hsm` estimates the remaining charge at the Standard B1 rate, capped at 730 hours. It skips pools with purge protection or a missing or elapsed scheduled purge date. Both fixes are review-only because purging permanently destroys the keys.

`azure-geo-redundant-nonprod` prices Hot, Cool, Cold and Archive block blob capacity on StorageV2 accounts tagged as non-production. It suggests dropping the secondary region while preserving local or zone redundancy. The change removes the account's ability to fail over to the paired region.

`azure-cold-hot-blob-account` targets StorageV2 accounts whose default access tier is Hot, with no lifecycle policy, Hot blob capacity reported on every day of the window, and no data-reading blob Transactions for 30 days (GetBlob and every operation not known to be metadata-only or a write counts as a read). The data that would move is a lower bound: Hot bytes minus the bytes written in the window and, for smart tier, minus 128 KiB per object (small objects stay hot). The saving is the Hot bill (graduated bands) minus the Hot bill for what stays and the Cool bill for what moves, less the smart-tier monitoring fee on every Hot object; Cold after 90 days is not counted. Smart tier is the fix on zone-redundant StorageV2 accounts without a hierarchical namespace, otherwise last access tracking plus a tierToCool lifecycle rule (a Cool write per blob once, shown in the evidence). When `azure-geo-redundant-nonprod` flags the same account, block-blob rates use that finding's target SKU. Findings under $5/month are omitted. Fixes are review-only. The ledger books when the account is deleted, the default tier becomes Smart, Cool or Cold, or a lifecycle policy appears.

`azure-cosmos-extra-regions-nonprod` flags Cosmos DB accounts tagged as non-production (the same tag keys as the geo-redundant storage check) that still replicate to more than one region. It prices a lower bound on savings from collapsing to the write region only: manual RU/s plus 10% of autoscale max RU/s, billed at each extra region's provisioned-throughput rate (and the write-region meter step-down when multi-region writes is on). Regional storage and zone-redundancy premiums are not priced. Serverless and single-region accounts are skipped; free tier and non-SQL APIs may still report unpriced.

`azure-disk-provisioned-performance` uses 30 full UTC days of daily maxima from the four Composite Disk metrics on Premium SSD v2 and Ultra disks attached to running VMs. It recommends lowering provisioned IOPS and MB/s to fit peaks with 50% headroom (rounded to 100 IOPS and 10 MB/s steps, with Azure's IOPS–throughput ratio and SKU minimums). Savings are the retail list price of billable provisioned IOPS and throughput above each meter's included amount. Findings under $5 a month are omitted. The ledger books when the disk is gone or both provisioned values are at or below the targets.

`azure-premium-fileshare-overprovisioned` targets classic premium FileStorage accounts (Premium_LRS or Premium_ZRS, not PremiumV2). It reads each share's FileCapacity, FileShareMaxUsedIOPS, FileShareMaxUsedBandwidthMiBps and FileSharePaidBurstingIOS over 30 full UTC days with hourly (PT1H) metrics filtered by share name. Any paid bursting in the window skips the share. The target GiB is the smallest whole quota that still covers data at 80% headroom and meets Azure's baseline IOPS and MiB/s formulas at 70% headroom, capped at today's quota. Savings are (quota − target) times the Premium Files provisioned GiB/month list price; findings under $10/month are omitted. The ledger books when the share is gone or its observed quota is at or under the target.

`azure-oversized-flexible-server` uses 30 full UTC days of daily maxima for CPU, memory, I/O and connections on PostgreSQL and MySQL flexible servers. It suggests one size down in the same General Purpose or Memory Optimized series. Projected CPU and I/O must stay at or below 50%, memory at or below 80%, and connections at or below half the target's default. I/O uses the larger of the vCore and IOPS-cap ratios when both IOPS caps are known. MySQL memory includes the InnoDB buffer pool. The check skips Burstable servers, servers with replication roles, unhealthy HA, and servers flagged idle.

`azure-flexible-server-ha-nonprod` flags running General Purpose or Memory Optimized PostgreSQL or MySQL flexible servers with zone-redundant or same-zone high availability, healthy HA state, and non-production environment tags. It skips replicas, Burstable tiers, protected tags, idle findings from the same scan, and servers without a retail standby-compute rate. Savings price one standby's compute at list price (standby storage is noted but not priced); when the oversized flexible-server check also reports the server, the standby is priced at that check's target SKU so the two savings add up. Disabling HA is review-only in fix bundles.

`azure-oversized-servicebus-premium` and `azure-oversized-eventhubs-premium` use the same 30-day window on namespace `NamespaceCpuUsage` and `NamespaceMemoryUsage` (Maximum, no dimension split). Each suggests one step down on the allowed Premium unit ladder when projected CPU and memory on the target stay at or below 50%. They skip capacity 1, geo-DR aliases, partitioned Service Bus namespaces, namespaces under autoscale, and idle findings from the same scan. Savings are list-priced unit hours × (current − target) capacity.

Savings cover compute at list price. PostgreSQL and MySQL HA each bill primary and standby compute at the same rate on the oversized check. A reservation can lower or strand the saving. The resize is review-only and restarts the server. The ledger verifies deletion or an observed SKU at or below the target in the same family and series.

`azure-oversized-redis` uses 30 full UTC days of daily maxima for usedmemoryRss, serverLoad and connectedclients on Standard C2–C6 and Premium P2–P5 caches (busiest shard, no dimension split). Peak used memory must stay at or below 50% of the target per-shard capacity (1 GB = 1e9 bytes), server load at or below 35%, and connections at or below 70% of the target's max. Premium geo-replication links must be absent (linkedServers readable). The check skips Basic, the smallest sizes, caches flagged idle, and caches with unread linkedServers. Savings are current minus target cache SKU list price, including Premium shard and extra-replica meters. The resize is review-only. The ledger verifies deletion or an observed SKU at or below the target in the same tier.

`azure-empty-virtual-hub` targets Standard Virtual WAN hubs that still bill with no hub virtual network connection, gateway, Azure Firewall, network virtual appliance or BGP peer. Basic hubs and Route Server hubs (no virtual WAN, with `ipConfigurations`) are out of scope. Savings use the Standard Hub Unit retail hourly rate for a full month; extra routing infrastructure units above the default deployment are noted in evidence but not priced. The verified-savings ledger records a fix only when the hub is gone. If the hub gains a connection, the finding clears but nothing is booked while the hub keeps billing.

`azure-acr-premium-unneeded` uses 30 full UTC days of hourly Total Transactions (peak requests per minute from the busiest hour, cap 2,500) and daily Maximum StorageUsed on Premium registries at least 30 days old. It skips registries using any Premium-only ARM feature (geo-replication, private endpoints, network rules, CMK, connected registries, agent pools, and similar). Savings are the Premium minus Standard registry unit list price minus billable Standard storage above 100 GiB (capped at the Premium 500 GiB included amount). Findings with non-positive savings are omitted. The ledger books when the registry is deleted or its observed SKU is Standard or Basic. Reader on the subscription covers the registry list and child collections this check reads.

`azure-hybrid-benefit-unused` flags running Windows VMs and Uniform scale sets paying the Windows Server licence in the PAYG compute rate (`licenseType` empty or `None`), and SQL VMs on `sqlServerLicenseType` PAYG with `sqlImageSku` Standard or Enterprise (Developer and Express are free; AHUB is already applied). Savings are only meaningful if you already own eligible Windows Server or SQL Server licences with Software Assurance or a subscription—it is a pricing-model finding, not idle waste. Windows savings use the difference between the Windows and Linux PAYG rates for the VM size and region. SQL savings use the global Virtual Machines Licenses meter for max(4, vCPU). Windows client images (`MicrosoftWindowsDesktop`) and VMs tagged for Azure Virtual Desktop are excluded. Reader on the subscription covers virtualMachines, virtualMachineScaleSets, and Microsoft.SqlVirtualMachine/sqlVirtualMachines.

The same check covers provisioned General Purpose and Business Critical vCore single databases, elastic pools and SQL Managed Instances with `LicenseIncluded` or an unset license type. It prices the SQL license meter at the hourly vCore rate × vCores × 730 and skips resources already flagged by an idle or oversized SQL check. Dev/Test subscriptions already waive this meter. With eligible SQL Server licences, switching to `BasePrice` requires no downtime. The fix bundle checks the current license type before applying and rolls back from `BasePrice` to `LicenseIncluded`.

`azure-firewall-premium-unneeded` uses 30 full UTC days of hourly Total DataProcessed on Premium firewalls with a firewall policy attached (not reported idle in the same run). It walks the policy and every base policy: TLS inspection, IDPS in Alert or Deny mode, transport-security certificate authority, or application rules with terminate TLS or target URLs block a finding. The busiest hour must convert to at or below 7.5 Gbit/s (a quarter of Standard's 30 Gbps rating). Savings are Premium minus Standard deployment-hour list price (VNet or Secured Virtual Hub meters); capacity-unit charges are excluded from the figure and noted in evidence. The ledger books when the firewall is deleted or its observed tier is Standard or Basic. Reader on the subscription covers azureFirewalls, firewallPolicies and ruleCollectionGroups.

`azure-unprovisioned-expressroute` flags service-provider ExpressRoute circuits still in NotProvisioned at the provider; Microsoft bills the monthly circuit fee from service-key issue until delete. Skips circuits younger than 14 days when `systemData.createdAt` is present. Prices the circuit meter from the peering location's Retail Prices zone.

`azure-idle-openai-ptu` requires 14 full UTC days of zero AzureOpenAIRequests for the deployment. It prices provisioned Global, Data Zone and Regional PTU hours at the account region's list rate. A covering PTU reservation prepays those hours, so deletion saves only once the reservation ends or is exchanged. The ledger records the list-price estimate after verifying deletion. The review script prints the model, version, SKU and capacity. Provisioned capacity may be unavailable when you try to recreate the deployment. Subscription Reader covers `Microsoft.CognitiveServices/accounts/read`, `Microsoft.CognitiveServices/accounts/deployments/read` and Monitor reads.

`azure-idle-container-app-min-replicas` requires 30 full UTC days of zero Requests on a Consumption app with ingress and only HTTP scale rules or no rules. The estimate uses idle CPU and memory list rates for the minimum replicas and leaves the monthly subscription free grant out of the calculation. Idle rates apply only while minimum replicas have very little CPU or network activity. The first request after scaling to zero can have a cold start. The ledger verifies zero minimum replicas or deletion. Subscription Reader covers `Microsoft.App/containerApps/read` and Monitor reads. The fix and rollback also require `Microsoft.App/containerApps/write`.

`azure-idle-ssis-integration-runtime` requires 14 full UTC days on a Started Azure-SSIS integration runtime, read from the factory's Monitor metrics filtered by `IntegrationRuntimeName`: `IntegrationRuntimeCpuPercentage` reported every day with every daily average at or under 5% (this also proves the runtime ran the whole window), and zero `SSISPackageExecutionSucceeded`, `SSISPackageExecutionFailed`, `SSISPackageExecutionCancel` and `SSISIntegrationRuntimeStartSucceeded` (a count series with no points is zero; a partial one is not judged). Savings price every node at the Azure Data Factory v2 SSIS list rate for its size, edition and license type; SSISDB on Azure SQL bills separately and is not counted. Packages started outside Data Factory may not show in the package metrics, so confidence is medium and stopping the runtime is review-only in fix bundles. Subscription Reader covers `Microsoft.DataFactory/factories/read`, `Microsoft.DataFactory/factories/integrationRuntimes/read` and the Monitor reads.

## 8. Privacy

GET-only to an allowlisted set of ARM routes. Metadata and metrics only; never data inside resources. Findings, history and the ledger stay on your machine. Outside Azure, the only optional call is a Slack webhook you pass yourself. Retail prices are read anonymously from `prices.azure.com`.

## 9. Troubleshooting

| FIX | What to do |
|---|---|
| `credentials` / `az login` | Run `az login` for the right tenant; on Pro use a user login, not a service principal |
| `grant Reader on subscription …` | Assign Reader at subscription scope (not only a resource group) |
| `use -tenant for this subscription's Entra tenant` | Pass that subscription's tenant GUID in `-tenant` |
| `service-principal … needs a Team license` | Upgrade, or use `-auth cli` |
| `Retail Prices API: unreachable` | Allow HTTPS to `prices.azure.com` |
| Provider NOTE, not FIX | Provider not registered; nothing to scan there |

See also the [Azure (preview)](../README.md#azure-preview) section in the README for the full check list.
