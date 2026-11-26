# CI gates (Team)

Run costhound in GitHub Actions and fail the job when waste crosses a limit. Team only. Copy the workflow for your cloud from [`docs/ci/`](ci/) into `.github/workflows/`.

## Gates and exit codes

| Flag | Fail when | Exit |
|---|---|---|
| `-fail-over N` | priced waste ≥ N $/month | **3** |
| `-fail-on-new` | priced waste that is new since the last run | **3** |
| `-fail-on-unowned N` | waste with no owner tag/label ≥ N $/month | **3** |
| `-slack-webhook URL` | (optional) post a summary; also `$COSTHOUND_SLACK_WEBHOOK` | failed post warns only |
| `-fail-scope` | all three clouds, with `-tf-state`: count `all` (default), `terraform`, or `outside` | used by the three gates |

Unset gates and no webhook → exit 0. A Pro or free key ignores the Team flags (prints a note, still exits 0).

**Azure and GCP only:** if any gate is set and the scan is incomplete (timeout/cancel, or unread/failed sources), costhound skips the gate and exits **1** so CI does not pass an incomplete report.

`-fail-on-new` needs a previous run in `-history-dir`. With no previous run (or `-no-history`), Azure/GCP print why and fail nothing; AWS only fails when a diff exists and new waste is > 0. If the history cache is missed (evicted or first run), `-fail-on-new` has nothing to compare with and will not catch new waste — keep `.costhound-runs` warm with a daily schedule (workflows below), or store it on a branch/artifact yourself.

## Outputs and history

- `-json > report.json` → write JSON to a file and upload it with the HTML (never print JSON on stdout: job logs would carry account, resource names and owners).
- `-html report.html` → interactive HTML (Pro/Team); upload it as an artifact.
- `-history-dir DIR` → compare and save runs (default: config dir `costhound/runs`). Persist `DIR` between workflow runs with `actions/cache` (workflows below use a daily cron and include `${{ github.run_attempt }}` in the cache key so a re-run does not restore that attempt's own empty history) or an artifact, or `-fail-on-new` has nothing to compare.

## License

Put a Team key in the GitHub secret `COSTHOUND_LICENSE`. costhound reads `$COSTHOUND_LICENSE` (or `-license`). Never echo the secret or print it in logs.

## Install

The workflows use the one-line installer (it downloads the release binary and checks `SHA256SUMS` the same way [`install.sh`](../install.sh) does):

```
curl -fsSL https://raw.githubusercontent.com/ThePun1sh3R/costhound/main/install.sh | sh
```

Pin with `COSTHOUND_VERSION=vX.Y.Z` if you want a fixed release. Default install dir is `~/.local/bin` (add it to `PATH` / `$GITHUB_PATH`).

## Cloud login (no long-lived cloud secrets)

### AWS — [`docs/ci/aws.yml`](ci/aws.yml)

- `aws-actions/configure-aws-credentials` with OIDC (`role-to-assume`).
- Attach this repo’s read-only [`iam-policy.json`](../iam-policy.json) to the role.
- Trust: GitHub’s OIDC provider → your role. **Check this in your setup** (account, repo, branch conditions).

### Azure — [`docs/ci/azure.yml`](ci/azure.yml)

- `azure/login` with OIDC (`client-id`, `tenant-id`, `subscription-id`).
- Then `costhound azure -auth cli …`. After `azure/login`, the Azure CLI is signed in; `-auth cli` works non-interactively on Team (Pro refuses a service-principal token even via CLI).
- `-auth service-principal` needs `-client-id` and `$AZURE_CLIENT_SECRET` (a long-lived secret). Prefer OIDC + `-auth cli` in Actions.
- Federated credential on the app registration: **check this in your setup**.

### GCP — [`docs/ci/gcp.yml`](ci/gcp.yml)

- `google-github-actions/auth` with Workload Identity Federation, then Application Default Credentials.
- Team allows service-account ADC; set `-quota-project` (or `$GOOGLE_CLOUD_QUOTA_PROJECT`).
- WIF pool/provider and SA bindings: **check this in your setup**.

## Secrets / vars (all three)

| Name | Required | Use |
|---|---|---|
| `COSTHOUND_LICENSE` | yes | Team key |
| `COSTHOUND_SLACK_WEBHOOK` | no | if set, costhound posts; if empty, no post |
| Cloud OIDC inputs | yes | role ARN / Azure app IDs / GCP WIF — see each workflow |

Tune `-fail-over` / `-fail-on-unowned` and schedule in the copy you commit. Action pins use current major tags (`@v4`, `@v2`); **check this in your setup** if your org pins digests instead.
