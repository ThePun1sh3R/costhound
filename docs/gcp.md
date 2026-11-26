# Google Cloud setup (preview)

First-time setup for scanning Google Cloud with costhound. Pro and Team only; the report is labelled GCP (preview).

## 1. What you need

- Application Default Credentials from your own user login: `gcloud auth application-default login`
- A Pro or Team license key (`-license`, `$COSTHOUND_LICENSE`, or `<config dir>/costhound/license`)
- Pro: up to 3 slots shared across AWS account IDs, Azure subscription IDs and GCP project IDs. The key must list each project. Pro accepts only your own user ADC (`authorized_user`), not a service account.
- Team: the key must list every project (there is no org-wide GCP cover). Team can also use a service account via ADC.

## 2. Least-privilege access

Grant **Viewer** (`roles/viewer`) and **Recommender Viewer** (`roles/recommender.viewer`) on each project you will scan. costhound only sends GET requests to Google APIs from a fixed allowlist, and never reads data inside resources.

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

Disks with a Cloud KMS or customer-supplied key need a decision (exit 4). Regional, Hyperdisk and pd-extreme disks stay review steps. Everything else is review-only; the bundle README says why (for example a disk kept by a stopped VM, or idle Cloud NAT).

## 7. Prices

USD list prices from Google's Cloud Billing Catalog. A dated catalog snapshot (2026-11-25) covers us-central1, us-east1, us-west1, europe-west1, europe-west4, asia-east1 and asia-southeast1. With `$COSTHOUND_GCP_CATALOG_KEY` set, live catalog prices are used for the same SKUs. Other regions and unsupported configurations stay unpriced. A missing catalog key is a NOTE in `-check`, not a FIX.

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
