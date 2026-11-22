#!/usr/bin/env bash
# Real-AWS smoke test. Creates a few tiny, tagged "waste" resources in YOUR
# account, runs costhound, checks it finds each one, then deletes everything it
# created, even if a step fails.
#
# Cost: a few cents for ~15 minutes (an ALB, a NAT gateway, two micro instances,
# 1 GiB disks, two IPs), covered by AWS Free plan credits.
#
# Usage: scripts/real-aws-smoke.sh <aws-profile> <expected-account-id>
# The account ID guard stops the script before it creates anything if the
# profile points at a different account than the one you meant.
set -euo pipefail
cd "$(dirname "$0")/.."

PROFILE=${1:?usage: $0 <aws-profile> <expected-account-id>}
EXPECT=${2:?usage: $0 <aws-profile> <expected-account-id>}
R=${SMOKE_REGION:-eu-west-1}
BIN=bin/costhound
RUN=costhound-smoke-$(date +%s)
export AWS_PROFILE=$PROFILE AWS_REGION=$R AWS_DEFAULT_REGION=$R AWS_PAGER=""
q() { aws "$@" --output text; }
log() { printf '\n== %s\n' "$*"; }

[ -x "$BIN" ] || { echo "build first: make build" >&2; exit 1; }
ACCOUNT=$(q sts get-caller-identity --query Account)
if [ "$ACCOUNT" != "$EXPECT" ]; then
  echo "profile $PROFILE is account $ACCOUNT, not $EXPECT: stopping, nothing was created" >&2
  exit 1
fi
mkdir -p .e2e
EXPECTS=.e2e/real-expect.txt
: > "$EXPECTS"
expect() { echo "$1|$2" >> "$EXPECTS"; }

INSTANCES=() VOLS=() SNAPS=() EIPS=() NATS=() LB="" TG="" LOGGROUP=""

cleanup() {
  set +e
  log "cleanup: deleting everything this run created ($RUN)"
  if [ ${#INSTANCES[@]} -gt 0 ]; then
    aws ec2 terminate-instances --instance-ids "${INSTANCES[@]}" > /dev/null && aws ec2 wait instance-terminated --instance-ids "${INSTANCES[@]}"
  fi
  if [ -n "$LB" ]; then
    aws elbv2 delete-load-balancer --load-balancer-arn "$LB" && aws elbv2 wait load-balancers-deleted --load-balancer-arns "$LB"
  fi
  # Right after the LB is gone, AWS can still call the target group "in use" for a while.
  if [ -n "$TG" ]; then
    for _ in $(seq 1 12); do aws elbv2 delete-target-group --target-group-arn "$TG" 2> /dev/null && break; sleep 5; done
  fi
  for n in "${NATS[@]}"; do
    aws ec2 delete-nat-gateway --nat-gateway-id "$n" > /dev/null
    for _ in $(seq 1 60); do
      [ "$(q ec2 describe-nat-gateways --nat-gateway-ids "$n" --query 'NatGateways[0].State')" = "deleted" ] && break
      sleep 5
    done
  done
  for a in "${EIPS[@]}"; do
    for _ in $(seq 1 12); do aws ec2 release-address --allocation-id "$a" 2> /dev/null && break; sleep 5; done
  done
  for v in "${VOLS[@]}"; do aws ec2 delete-volume --volume-id "$v"; done
  for s in "${SNAPS[@]}"; do aws ec2 delete-snapshot --snapshot-id "$s"; done
  [ -n "$LOGGROUP" ] && aws logs delete-log-group --log-group-name "$LOGGROUP"
  log "checking nothing from $RUN is left"
  local f="Name=tag:costhound-smoke,Values=$RUN" left="" errs="" o
  # A check that fails proves nothing: it's reported, never read as "clean".
  add() { if o=$(q "$@" 2>&1); then left+=" $o"; else errs+="$o"$'\n'; fi; }
  add ec2 describe-instances --filters "$f" Name=instance-state-name,Values=pending,running,stopping,stopped --query 'Reservations[].Instances[].InstanceId'
  add ec2 describe-volumes --filters "$f" --query 'Volumes[].VolumeId'
  add ec2 describe-snapshots --owner-ids self --filters "$f" --query 'Snapshots[].SnapshotId'
  add ec2 describe-addresses --filters "$f" --query 'Addresses[].AllocationId'
  add ec2 describe-nat-gateways --filter "$f" Name=state,Values=pending,available,deleting --query 'NatGateways[].NatGatewayId'
  [ -n "$LOGGROUP" ] && add logs describe-log-groups --log-group-name-prefix "$LOGGROUP" --query 'logGroups[].logGroupName'
  # A deleted load balancer or target group answers NotFound: that's gone.
  [ -n "$LB" ] && left+=" $(q elbv2 describe-load-balancers --load-balancer-arns "$LB" --query 'LoadBalancers[].LoadBalancerName' 2> /dev/null)"
  [ -n "$TG" ] && left+=" $(q elbv2 describe-target-groups --target-group-arns "$TG" --query 'TargetGroups[].TargetGroupName' 2> /dev/null)"
  left=$(echo "$left" | tr -s ' \t' '\n' | grep -v '^$' || true)
  if [ -n "$errs" ]; then echo "COULD NOT CHECK what is left (look in the console):"; echo "$errs"; fi
  if [ -n "$left" ]; then echo "LEFT BEHIND, delete by hand:"; echo "$left"; elif [ -z "$errs" ]; then echo "clean: nothing left"; fi
}
trap cleanup EXIT

TAGS="{Key=costhound-smoke,Value=$RUN}"
log "account $ACCOUNT, region $R, run $RUN"

AMI=$(q ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 --query Parameter.Value)

log "stopped instance with a gp2 disk (t3.micro)"
I=$(q ec2 run-instances --image-id "$AMI" --instance-type t3.micro \
  --block-device-mappings 'DeviceName=/dev/xvda,Ebs={VolumeType=gp2,VolumeSize=8,DeleteOnTermination=true}' \
  --tag-specifications "ResourceType=instance,Tags=[$TAGS]" "ResourceType=volume,Tags=[$TAGS]" --query 'Instances[0].InstanceId')
INSTANCES+=("$I")
aws ec2 wait instance-running --instance-ids "$I"
aws ec2 stop-instances --instance-ids "$I" > /dev/null
aws ec2 wait instance-stopped --instance-ids "$I"
expect stopped-instance "$I"

log "running old-generation instance with a gp2 disk (t2.micro, skipped if the account plan doesn't allow it)"
if I2=$(q ec2 run-instances --image-id "$AMI" --instance-type t2.micro \
  --block-device-mappings 'DeviceName=/dev/xvda,Ebs={VolumeType=gp2,VolumeSize=8,DeleteOnTermination=true}' \
  --tag-specifications "ResourceType=instance,Tags=[$TAGS]" "ResourceType=volume,Tags=[$TAGS]" --query 'Instances[0].InstanceId'); then
  INSTANCES+=("$I2")
  aws ec2 wait instance-running --instance-ids "$I2"
  expect old-instance-type "$I2"
  expect gp2-to-gp3 "$(q ec2 describe-volumes --filters Name=attachment.instance-id,Values="$I2" --query 'Volumes[0].VolumeId')"
else
  echo "t2.micro not allowed here: skipping the old-instance-type and gp2-to-gp3 checks"
fi

log "unattached 1 GiB gp2 volume"
V=$(q ec2 create-volume --availability-zone "${R}a" --size 1 --volume-type gp2 --tag-specifications "ResourceType=volume,Tags=[$TAGS]" --query VolumeId)
VOLS+=("$V")
expect unattached-ebs "$V"

log "snapshot of a volume that is then deleted"
V2=$(q ec2 create-volume --availability-zone "${R}a" --size 1 --volume-type gp3 --tag-specifications "ResourceType=volume,Tags=[$TAGS]" --query VolumeId)
VOLS+=("$V2")
aws ec2 wait volume-available --volume-ids "$V2"
S=$(q ec2 create-snapshot --volume-id "$V2" --tag-specifications "ResourceType=snapshot,Tags=[$TAGS]" --query SnapshotId)
SNAPS+=("$S")
aws ec2 wait snapshot-completed --snapshot-ids "$S"
aws ec2 delete-volume --volume-id "$V2"
keep=()
for v in "${VOLS[@]}"; do if [ "$v" != "$V2" ]; then keep+=("$v"); fi; done
VOLS=("${keep[@]}")
expect old-snapshots "$S"

log "Elastic IP attached to nothing"
EIP=$(q ec2 allocate-address --domain vpc --tag-specifications "ResourceType=elastic-ip,Tags=[$TAGS]" --query AllocationId)
EIPS+=("$EIP")
expect idle-ip "$EIP"

log "log group that never expires"
LOGGROUP=/costhound-smoke/$RUN
aws logs create-log-group --log-group-name "$LOGGROUP" --tags "costhound-smoke=$RUN"
expect log-retention "$LOGGROUP"

VPC=$(q ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId')
if [ -n "$VPC" ] && [ "$VPC" != "None" ]; then
  read -r SUB1 SUB2 _ <<< "$(q ec2 describe-subnets --filters Name=vpc-id,Values="$VPC" Name=default-for-az,Values=true --query 'Subnets[0:2].SubnetId')"

  log "load balancer with no targets"
  LB=$(q elbv2 create-load-balancer --name "cs-${RUN##*-}" --subnets "$SUB1" "$SUB2" --tags "Key=costhound-smoke,Value=$RUN" --query 'LoadBalancers[0].LoadBalancerArn')
  TG=$(q elbv2 create-target-group --name "cs-tg-${RUN##*-}" --protocol HTTP --port 80 --vpc-id "$VPC" --tags "Key=costhound-smoke,Value=$RUN" --query 'TargetGroups[0].TargetGroupArn')
  aws elbv2 create-listener --load-balancer-arn "$LB" --protocol HTTP --port 80 --default-actions Type=forward,TargetGroupArn="$TG" > /dev/null
  expect idle-load-balancer "cs-${RUN##*-}"

  log "NAT gateway in a VPC without the S3 endpoint (skipped if the VPC already has one)"
  if [ "$(q ec2 describe-vpc-endpoints --filters Name=vpc-id,Values="$VPC" Name=vpc-endpoint-type,Values=Gateway --query "length(VpcEndpoints[?ends_with(ServiceName, '.s3')])")" = "0" ]; then
    NAT_EIP=$(q ec2 allocate-address --domain vpc --tag-specifications "ResourceType=elastic-ip,Tags=[$TAGS]" --query AllocationId)
    EIPS+=("$NAT_EIP")
    N=$(q ec2 create-nat-gateway --subnet-id "$SUB1" --allocation-id "$NAT_EIP" --tag-specifications "ResourceType=natgateway,Tags=[$TAGS]" --query 'NatGateway.NatGatewayId')
    NATS+=("$N")
    aws ec2 wait nat-gateway-available --nat-gateway-ids "$N"
    expect nat-no-s3-endpoint "$VPC"
  fi
else
  echo "no default VPC: skipping the load balancer and NAT checks"
fi

log "running costhound"
"$BIN" -profile "$PROFILE" -regions "$R" -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/real-report.txt
"$BIN" -profile "$PROFILE" -regions "$R" -json -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/real-report.json 2> /dev/null

# Paid checks, when the seller's signing key is on this machine: a Team key
# for this account, judged with a 0-day window so fresh resources count.
SIGNKEY=${COSTHOUND_SIGNING_KEY:-$HOME/.config/costhound-seller/signing.key}
if [ -f "$SIGNKEY" ] && [ ${#NATS[@]} -gt 0 ]; then
  go build -tags pro -o bin/costhound-license ./cmd/costhound-license
  KEY=$(bin/costhound-license issue -key "$SIGNKEY" -plan team -accounts "$ACCOUNT" -id "smoke-$RUN")
  "$BIN" -profile "$PROFILE" -regions "$R" -license "$KEY" -usage-days 0 -json -no-history -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/real-report-team.json 2> /dev/null
  python3 - "${NATS[0]}" <<'PY'
import json, sys
r = json.load(open(".e2e/real-report-team.json"))
# A NAT gateway nothing routes to is nat-no-routes' (it took those over from
# idle-nat); a routed one with no traffic is idle-nat's.
ok = r["plan"] == "team" and any(f["check"] in ("idle-nat", "nat-no-routes") and sys.argv[1] in f["resource"] for f in r["findings"])
print(("SMOKE PASS" if ok else "SMOKE FAIL") + f" (team plan): the idle NAT gateway {sys.argv[1]} {'found' if ok else 'missed'}; skipped: {r['skipped']}")
sys.exit(0 if ok else 1)
PY

  # The fix bundle on real AWS: costhound plan for this run's unattached disk,
  # idle IP and empty load balancer only (-only), then each fix's own apply.sh.
  # A fix may stop and ask for a decision (exit 4): that's a guard working,
  # reported, not a failure. The trap deletes whatever is left, the disk's
  # backup snapshot included.
  log "fix bundle: plan and apply the fixes for this run's disk, IP and load balancer"
  IDS=$(python3 - "$V" "$EIP" "$LB" <<'PY'
import json, sys
vol, eip, lb = sys.argv[1:4]
r = json.load(open(".e2e/real-report-team.json"))
ids = [f["id"] for f in r["findings"] if (f["check"], (f.get("facts") or {}).get({"unattached-ebs": "volume", "idle-ip": "allocation", "idle-load-balancer": "lb_arn"}.get(f["check"], "-")))
       in {("unattached-ebs", vol), ("idle-ip", eip), ("idle-load-balancer", lb)}]
print(",".join(ids))
PY
  )
  rm -rf .e2e/smoke-fixes
  "$BIN" plan -from .e2e/real-report-team.json -out .e2e/smoke-fixes -only "$IDS" -license "$KEY" > .e2e/smoke-plan.txt
  RESULTS=()
  for d in .e2e/smoke-fixes/*/*/; do
    [ -x "$d/apply.sh" ] || continue
    set +e
    (cd "$d" && COSTHOUND_CONFIRM=yes ./apply.sh) > "$d/apply.log" 2>&1
    code=$?
    set -e
    RESULTS+=("$(basename "$d")=$code")
    tail -2 "$d/apply.log" | sed 's/^/    /'
  done
  # Gone only when AWS says NotFound: a failed or throttled check isn't proof.
  gone() {
    local out
    if out=$("$@" 2>&1); then return 1; fi
    case "$out" in *NotFound*) return 0 ;; esac
    echo "    could not check: $out" >&2
    return 1
  }
  BAD=()
  gone aws ec2 describe-volumes --volume-ids "$V" || BAD+=("disk $V still there")
  gone aws ec2 describe-addresses --allocation-ids "$EIP" || BAD+=("IP $EIP still allocated")
  # Cleanup bookkeeping: what the bundle deleted, and the backup it made.
  if gone aws ec2 describe-volumes --volume-ids "$V"; then
    keep=()
    for v in "${VOLS[@]}"; do if [ "$v" != "$V" ]; then keep+=("$v"); fi; done
    VOLS=("${keep[@]}")
    for s in $(q ec2 describe-snapshots --owner-ids self --filters "Name=tag:costhound-backup-of,Values=$V" --query 'Snapshots[].SnapshotId'); do SNAPS+=("$s"); done
  fi
  if gone aws ec2 describe-addresses --allocation-ids "$EIP"; then
    keep=()
    for a in "${EIPS[@]}"; do if [ "$a" != "$EIP" ]; then keep+=("$a"); fi; done
    EIPS=("${keep[@]}")
  fi
  if [ -n "$LB" ] && gone aws elbv2 describe-load-balancers --load-balancer-arns "$LB"; then LB=""; fi
  if [ ${#BAD[@]} -gt 0 ] || [ ${#RESULTS[@]} -eq 0 ]; then
    echo "SMOKE FAIL (fix bundle): ${RESULTS[*]:-no fixes written}; ${BAD[*]:-}"
    exit 1
  fi
  echo "SMOKE PASS (fix bundle): ${RESULTS[*]} (0 = applied, 4 = asked for a decision); the disk and the IP are gone on re-check"
fi

python3 - "$EXPECTS" <<'PY'
import json, sys
want = [l.strip().split("|", 1) for l in open(sys.argv[1]) if l.strip()]
r = json.load(open(".e2e/real-report.json"))
missing = [f"{c}: {n}" for c, n in want
           if not any(f["check"] == c and n in json.dumps(f) for f in r["findings"])]
if r["skipped"]:
    print("could not read:", r["skipped"])
if missing:
    print("SMOKE FAIL, not found:\n  " + "\n  ".join(missing)); sys.exit(1)
print(f"SMOKE PASS: found all {len(want)} planted resources")
PY
echo "full report: .e2e/real-report.txt"
