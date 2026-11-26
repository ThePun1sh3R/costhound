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

Disks with a customer-managed key, Azure Disk Encryption, Trusted Launch or confidential VM security type, or private-link-only access need a decision (exit 4). Ultra Disk and Premium SSD v2 stay review steps. Everything else is review-only; the bundle README says why (for example a disk kept by a deallocated VM, an empty load balancer, or an idle NAT gateway).

## 7. Prices

USD list prices from the public Azure Retail Prices API (`prices.azure.com`). A missing or ambiguous meter leaves a finding unpriced.

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
