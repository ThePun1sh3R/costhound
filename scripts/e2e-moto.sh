#!/usr/bin/env bash
# End-to-end test: run the real costhound binary against moto, a local AWS
# emulator, seeded with known waste. It never touches real AWS: every call goes
# to 127.0.0.1 with dummy credentials and no AWS config files.
#
# Needs: python3, aws CLI v2, bin/costhound (make build).
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${E2E_PORT:-5055}
VENV=.e2e/venv
BIN=bin/costhound
R=eu-west-1

[ -x "$BIN" ] || { echo "build first: make build" >&2; exit 1; }
if [ ! -x "$VENV/bin/moto_server" ]; then
  python3 -m venv "$VENV"
  "$VENV/bin/pip" install -q "moto[server]"
fi

# Moto can't stop Transfer servers. Seed the stopped state through its model.
# Its DMS model emits ISO dates where AWS JSON 1.1 expects epoch seconds.
# Backdate only the planted DMS instance so Free can judge it without waiting.
# Backdate a Private CA tagged e2e-age=old past the 30-day free trial.
"$VENV/bin/python" - "$PORT" > .e2e/moto.log 2>&1 <<'PYMOTO' &
import sys
from datetime import datetime, timedelta, timezone

from moto.acmpca.models import CertificateAuthority, acmpca_backends
from moto.dms.models import FakeReplicationInstance
from moto.server import main
from moto.transfer.types import Server, ServerState

init_server = Server.__post_init__
def init_fixture_server(self):
    init_server(self)
    if {"Key": "e2e-state", "Value": "stopped"} in self.tags:
        self.state = ServerState.OFFLINE
Server.__post_init__ = init_fixture_server

instance_dict = FakeReplicationInstance.to_dict
def dms_fixture_dict(self):
    response = instance_dict(self)
    created = self.creation_date
    if created.tzinfo is None:
        created = created.replace(tzinfo=timezone.utc)
    if self.id == "e2e-dms-idle":
        created -= timedelta(days=8)
    response["InstanceCreateTime"] = created.timestamp()
    return response
FakeReplicationInstance.to_dict = dms_fixture_dict

ca_to_json = CertificateAuthority.to_json
def pca_fixture_json(self):
    response = ca_to_json(self)
    backend = acmpca_backends[self.account_id][self.region_name]
    tags = backend.tagger.get_tag_dict_for_resource(self.arn)
    if tags.get("e2e-age") == "old":
        response["CreatedAt"] = (datetime.now(timezone.utc) - timedelta(days=40)).timestamp()
        response["UsageMode"] = "GENERAL_PURPOSE"  # moto hardcodes SHORT_LIVED_CERTIFICATE; AWS defaults to this
    return response
CertificateAuthority.to_json = pca_fixture_json

main(["-H", "127.0.0.1", "-p", sys.argv[1]])
PYMOTO
MOTO=$!
trap 'kill $MOTO 2>/dev/null; wait $MOTO 2>/dev/null || true' EXIT
for _ in $(seq 1 40); do curl -s -o /dev/null "http://127.0.0.1:$PORT/moto-api/" && break; sleep 0.25; done

unset AWS_PROFILE
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=$R AWS_REGION=$R \
  AWS_ENDPOINT_URL=http://127.0.0.1:$PORT AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_PAGER=""
q() { aws "$@" --output text; }

echo "seeding moto with known waste..."
VOL=$(q ec2 create-volume --availability-zone ${R}a --size 100 --volume-type gp2 --tag-specifications 'ResourceType=volume,Tags=[{Key=Team,Value=payments}]' --query VolumeId)
V=$(q ec2 create-volume --availability-zone ${R}a --size 8 --volume-type gp3 --query VolumeId)
SNAP=$(q ec2 create-snapshot --volume-id "$V" --query SnapshotId)
aws ec2 delete-volume --volume-id "$V"
AMI=$(q ec2 describe-images --owners amazon --query 'Images[0].ImageId')
OLD=$(q ec2 run-instances --image-id "$AMI" --instance-type t2.micro --count 1 --query 'Instances[0].InstanceId')
STOPPED=$(q ec2 run-instances --image-id "$AMI" --instance-type m5.large --count 1 --query 'Instances[0].InstanceId')
# Detailed monitoring on (Graviton, so nothing else flags it).
MONITORED=$(q ec2 run-instances --image-id "$AMI" --instance-type t4g.micro --count 1 --monitoring Enabled=true --query 'Instances[0].InstanceId')
aws ec2 stop-instances --instance-ids "$STOPPED" > /dev/null
EIP=$(q ec2 allocate-address --domain vpc --query AllocationId)
aws logs create-log-group --log-group-name /e2e/never-expires
aws logs create-log-group --log-group-name /e2e/has-retention
aws logs put-retention-policy --log-group-name /e2e/has-retention --retention-in-days 14
# EKS control-plane logs: of a cluster EKS no longer has (left behind), and of e2e-busy-eks (still running).
aws logs create-log-group --log-group-name /aws/eks/e2e-deleted-eks/cluster
aws logs create-log-group --log-group-name /aws/eks/e2e-busy-eks/cluster
VPC=$(q ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId')
read -r SUB1 SUB2 _ <<< "$(q ec2 describe-subnets --filters Name=vpc-id,Values="$VPC" --query 'Subnets[0:2].SubnetId')"
LB=$(q elbv2 create-load-balancer --name e2e-idle-alb --subnets "$SUB1" "$SUB2" --query 'LoadBalancers[0].LoadBalancerArn')
TG=$(q elbv2 create-target-group --name e2e-empty --protocol HTTP --port 80 --vpc-id "$VPC" --query 'TargetGroups[0].TargetGroupArn')
aws elbv2 create-listener --load-balancer-arn "$LB" --protocol HTTP --port 80 --default-actions Type=forward,TargetGroupArn="$TG" > /dev/null
NAT_EIP=$(q ec2 allocate-address --domain vpc --query AllocationId)
NATGW=$(q ec2 create-nat-gateway --subnet-id "$SUB1" --allocation-id "$NAT_EIP" --query NatGateway.NatGatewayId)
# A second NAT gateway that a route uses (nat-no-routes must stay quiet about it).
NAT2_EIP=$(q ec2 allocate-address --domain vpc --query AllocationId)
NATGW2=$(q ec2 create-nat-gateway --subnet-id "$SUB2" --allocation-id "$NAT2_EIP" --query NatGateway.NatGatewayId)
RT=$(q ec2 create-route-table --vpc-id "$VPC" --query RouteTable.RouteTableId)
aws ec2 create-route --route-table-id "$RT" --destination-cidr-block 0.0.0.0/0 --nat-gateway-id "$NATGW2" > /dev/null

# More free checks: an io1 disk that fits gp3, a secret nobody reads, an ECR
# repo with no lifecycle policy, an x86 Lambda, a bucket with no rules.
IO1=$(q ec2 create-volume --availability-zone ${R}a --size 100 --volume-type io1 --iops 4000 --query VolumeId)
aws ec2 attach-volume --volume-id "$IO1" --instance-id "$OLD" --device /dev/sdf > /dev/null
# A Multi-Attach io1 disk over 32,000 IOPS: gp3 can't take it, io2 costs less.
IO1MA=$(q ec2 create-volume --availability-zone ${R}a --size 1000 --volume-type io1 --iops 40000 --multi-attach-enabled --query VolumeId)
aws ec2 attach-volume --volume-id "$IO1MA" --instance-id "$OLD" --device /dev/sdg > /dev/null
# A REST API with two cached stages: caching off everywhere ("cold": billed
# for nothing) and on ("warm"). The stage variable must never be read.
APIID=$(q apigateway create-rest-api --name e2e-cached-api --query id)
APIROOT=$(q apigateway get-resources --rest-api-id "$APIID" --query 'items[0].id')
aws apigateway put-method --rest-api-id "$APIID" --resource-id "$APIROOT" --http-method GET --authorization-type NONE > /dev/null
aws apigateway put-integration --rest-api-id "$APIID" --resource-id "$APIROOT" --http-method GET --type MOCK > /dev/null
APIDEP=$(q apigateway create-deployment --rest-api-id "$APIID" --query id)
for st in cold warm; do
  aws apigateway create-stage --rest-api-id "$APIID" --stage-name $st --deployment-id "$APIDEP" --cache-cluster-enabled --cache-cluster-size 0.5 \
    --variables db_password=e2e-stage-variable-secret > /dev/null
done
aws apigateway update-stage --rest-api-id "$APIID" --stage-name cold --patch-operations 'op=replace,path=/*/*/caching/enabled,value=false' > /dev/null
aws apigateway update-stage --rest-api-id "$APIID" --stage-name warm --patch-operations 'op=replace,path=/*/*/caching/enabled,value=true' > /dev/null
# An EMR cluster with a password in its configuration, a step's arguments and
# its Kerberos settings: the paid scans read it and must keep none of them.
# An ECS Fargate service; its task definition holds a secret in a container's
# environment, which costhound must never keep (moto needs securityGroups).
ECSVPC=$(q ec2 create-vpc --cidr-block 10.42.0.0/16 --query Vpc.VpcId)
ECSSUB=$(q ec2 create-subnet --vpc-id "$ECSVPC" --cidr-block 10.42.1.0/24 --availability-zone ${R}a --query Subnet.SubnetId)
ECSSG=$(q ec2 create-security-group --group-name e2e-ecs --description e2e --vpc-id "$ECSVPC" --query GroupId)
aws ecs create-cluster --cluster-name e2e-ecs > /dev/null
aws ecs register-task-definition --family e2e-web --requires-compatibilities FARGATE --network-mode awsvpc --cpu 1024 --memory 4096 \
  --runtime-platform cpuArchitecture=X86_64,operatingSystemFamily=LINUX \
  --container-definitions '[{"name":"web","image":"nginx","essential":true,"environment":[{"name":"DB_PASSWORD","value":"e2e-ecs-env-secret-value"}]}]' > /dev/null
aws ecs create-service --cluster e2e-ecs --service-name e2e-web --task-definition e2e-web --desired-count 2 --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$ECSSUB],securityGroups=[$ECSSG]}" > /dev/null
aws emr create-cluster --name e2e-emr --release-label emr-7.0.0 --instance-type m5.xlarge --instance-count 1 --applications Name=Spark \
  --configurations '[{"Classification":"hive-site","Properties":{"javax.jdo.option.ConnectionPassword":"e2e-hive-secret-value"}}]' \
  --steps 'Type=CUSTOM_JAR,Name=load,ActionOnFailure=CONTINUE,Jar=command-runner.jar,Args=[echo,e2e-step-secret-value]' \
  --kerberos-attributes Realm=EC2.INTERNAL,KdcAdminPassword=e2e-kdc-secret-value \
  --service-role EMR_DefaultRole --ec2-attributes InstanceProfile=EMR_EC2_DefaultRole --no-auto-terminate > /dev/null
# Two trails copying the same management events here: the single-Region one pays.
aws s3api create-bucket --bucket e2e-trail-logs --create-bucket-configuration LocationConstraint=$R > /dev/null
aws cloudtrail create-trail --name e2e-all-regions --s3-bucket-name e2e-trail-logs --is-multi-region-trail > /dev/null
aws cloudtrail create-trail --name e2e-trail-copy --s3-bucket-name e2e-trail-logs > /dev/null
for t in e2e-all-regions e2e-trail-copy; do
  aws cloudtrail put-event-selectors --trail-name $t --event-selectors '[{"ReadWriteType":"All","IncludeManagementEvents":true}]' > /dev/null
  aws cloudtrail start-logging --name $t
done
aws secretsmanager create-secret --name e2e/never-read --secret-string x > /dev/null
aws ecr create-repository --repository-name e2e-app > /dev/null
ROLE=$(q iam create-role --role-name e2e-lambda --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}' --query Role.Arn)
python3 -c "import zipfile; z=zipfile.ZipFile('.e2e/fn.zip','w'); z.writestr('index.py','def handler(e,c): return 1'); z.close()"
aws lambda create-function --function-name e2e-x86 --runtime python3.12 --handler index.handler --role "$ROLE" --zip-file fileb://.e2e/fn.zip --architectures x86_64 \
  --environment 'Variables={DB_PASSWORD=e2e-lambda-env-secret-value}' > /dev/null
aws s3api create-bucket --bucket e2e-no-rules --create-bucket-configuration LocationConstraint=$R > /dev/null
aws dynamodb create-table --tags Key=owner,Value=data-eng --table-name e2e-orders --attribute-definitions AttributeName=id,AttributeType=S --key-schema AttributeName=id,KeyType=HASH --provisioned-throughput ReadCapacityUnits=100,WriteCapacityUnits=50 > /dev/null
aws elasticache create-cache-cluster --cache-cluster-id e2e-old-cache --engine memcached --cache-node-type cache.t2.micro --num-cache-nodes 1 > /dev/null
S3IFACE=$(q ec2 create-vpc-endpoint --vpc-id "$VPC" --vpc-endpoint-type Interface --service-name com.amazonaws.$R.s3 --subnet-ids "$SUB1" --query VpcEndpoint.VpcEndpointId)

# Long-tail free checks: an AMI nothing launches (and one a running instance
# uses, which must stay quiet), an empty Dedicated Host, a disabled KMS key
# (and an enabled one), and a snapshot of a database that was deleted.
UNUSED_AMI=$(q ec2 create-image --instance-id "$OLD" --name e2e-unused-ami --query ImageId)
TPL_AMI=$(q ec2 create-image --instance-id "$OLD" --name e2e-used-ami --query ImageId)
aws ec2 run-instances --image-id "$TPL_AMI" --instance-type t3.micro --count 1 > /dev/null
DHOST=$(q ec2 allocate-hosts --instance-family m5 --availability-zone ${R}a --quantity 1 --query 'HostIds[0]')
KMSKEY=$(q kms create-key --description e2e-disabled --query KeyMetadata.KeyId)
aws kms disable-key --key-id "$KMSKEY"
KMSON=$(q kms create-key --description e2e-enabled --query KeyMetadata.KeyId)
aws rds create-db-instance --db-instance-identifier e2e-gone --db-instance-class db.t3.micro --engine postgres --allocated-storage 20 --master-username e2e --master-user-password e2e-password-1 > /dev/null
aws rds create-db-snapshot --db-instance-identifier e2e-gone --db-snapshot-identifier e2e-gone-final > /dev/null
aws rds delete-db-instance --db-instance-identifier e2e-gone --skip-final-snapshot > /dev/null
# A Classic Load Balancer with no instances (the old-class database is planted further down).
# A versioned bucket that keeps every old version, and one with a rule that expires them (quiet).
aws s3api create-bucket --bucket e2e-versioned --create-bucket-configuration LocationConstraint=$R > /dev/null
aws s3api put-bucket-versioning --bucket e2e-versioned --versioning-configuration Status=Enabled
aws s3api create-bucket --bucket e2e-versioned-ok --create-bucket-configuration LocationConstraint=$R > /dev/null
aws s3api put-bucket-versioning --bucket e2e-versioned-ok --versioning-configuration Status=Enabled
aws s3api put-bucket-lifecycle-configuration --bucket e2e-versioned-ok --lifecycle-configuration '{"Rules":[{"ID":"old","Status":"Enabled","Filter":{},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
# Route 53: a health check nothing uses, and one a record routes on (quiet).
HC=$(q route53 create-health-check --caller-reference e2e-hc-1 --health-check-config Type=HTTPS,FullyQualifiedDomainName=unused.e2e.example,Port=443,ResourcePath=/ --query HealthCheck.Id)
HC2=$(q route53 create-health-check --caller-reference e2e-hc-2 --health-check-config Type=HTTP,FullyQualifiedDomainName=used.e2e.example,Port=80 --query HealthCheck.Id)
ZONE=$(q route53 create-hosted-zone --name e2e.example --caller-reference e2e-zone --query HostedZone.Id)
aws route53 change-resource-record-sets --hosted-zone-id "$ZONE" --change-batch '{"Changes":[{"Action":"CREATE","ResourceRecordSet":{"Name":"www.e2e.example","Type":"A","SetIdentifier":"primary","Failover":"PRIMARY","TTL":60,"ResourceRecords":[{"Value":"192.0.2.10"}],"HealthCheckId":"'"$HC2"'"}}]}' > /dev/null
# An empty public zone (only NS and SOA), and an empty private one (quiet: it answers NXDOMAIN in its VPC).
EMPTYZONE=$(q route53 create-hosted-zone --name e2e-empty.example --caller-reference e2e-empty-zone --query HostedZone.Id)
PRIVZONE=$(q route53 create-hosted-zone --name e2e-private.example --caller-reference e2e-private-zone --vpc VPCRegion=$R,VPCId=$VPC --hosted-zone-config PrivateZone=true --query HostedZone.Id)
aws elb create-load-balancer --load-balancer-name e2e-clb --listeners Protocol=HTTP,LoadBalancerPort=80,InstanceProtocol=HTTP,InstancePort=80 --subnets "$SUB1" > /dev/null
# Paid long tail: an Elasticsearch 7.4 domain (in extended support, which
# costs its instance price again since 2026-11-07), and a SageMaker endpoint,
# a Redshift cluster and a Transit Gateway attachment, so their loaders run.
aws opensearch create-domain --domain-name e2e-old-search --engine-version Elasticsearch_7.4 --cluster-config InstanceType=r6g.large.search,InstanceCount=2 --ebs-options EBSEnabled=true,VolumeType=gp3,VolumeSize=20 > /dev/null
aws sagemaker create-model --model-name e2e-model --execution-role-arn arn:aws:iam::123456789012:role/e2e-sm --primary-container Image=123456789012.dkr.ecr.$R.amazonaws.com/e2e:latest > /dev/null
aws sagemaker create-endpoint-config --endpoint-config-name e2e-cfg --production-variants VariantName=AllTraffic,ModelName=e2e-model,InstanceType=ml.m5.large,InitialInstanceCount=1 > /dev/null
aws sagemaker create-endpoint --endpoint-name e2e-endpoint --endpoint-config-name e2e-cfg > /dev/null
aws redshift create-cluster --cluster-identifier e2e-wh --node-type ra3.xlplus --number-of-nodes 2 --master-username e2eadmin --master-user-password E2epassw0rd > /dev/null
TGW=$(q ec2 create-transit-gateway --query TransitGateway.TransitGatewayId)
aws ec2 create-transit-gateway-vpc-attachment --transit-gateway-id "$TGW" --vpc-id "$VPC" --subnet-ids "$SUB1" > /dev/null
# An EKS cluster with nothing to run on (and one with a node group, which must stay quiet).
aws eks create-cluster --name e2e-empty-eks --role-arn arn:aws:iam::123456789012:role/e2e-eks --resources-vpc-config subnetIds="$SUB1","$SUB2" > /dev/null
aws eks create-cluster --name e2e-busy-eks --role-arn arn:aws:iam::123456789012:role/e2e-eks --resources-vpc-config subnetIds="$SUB1","$SUB2" > /dev/null
aws eks create-nodegroup --cluster-name e2e-busy-eks --nodegroup-name ng --node-role arn:aws:iam::123456789012:role/e2e-node --subnets "$SUB1" > /dev/null

# Seed the read-only Transfer and DMS inventory with the real Moto create APIs.
TRANSFER=$(q transfer create-server --domain S3 --endpoint-type PUBLIC --identity-provider-type SERVICE_MANAGED \
  --protocols SFTP --tags Key=e2e-state,Value=stopped --query ServerId)
aws transfer create-user --server-id "$TRANSFER" --user-name e2e-transfer-user --role "$ROLE" > /dev/null
DMS=$(q dms create-replication-instance --replication-instance-identifier e2e-dms-idle \
  --replication-instance-class dms.r5.large --allocated-storage 100 --no-multi-az --no-publicly-accessible \
  --query ReplicationInstance.ReplicationInstanceArn)
# moto has create/describe file systems (no Lifecycle/CreationTime); it lacks DescribeVolumes and DescribeDataRepositoryAssociations.
aws fsx create-file-system --file-system-type LUSTRE --storage-capacity 1200 --subnet-ids "$SUB1" \
  --lustre-configuration DeploymentType=SCRATCH_2 > /dev/null
PCA=$(q acm-pca create-certificate-authority --certificate-authority-type ROOT \
  --certificate-authority-configuration 'KeyAlgorithm=RSA_2048,SigningAlgorithm=SHA256WITHRSA,Subject={CommonName=e2e-pca}' \
  --tags Key=e2e-age,Value=old --query CertificateAuthorityArn)

echo "running costhound..."
"$BIN" -regions $R -json -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -secret-days 0 -min-usd 0 > .e2e/report.json

python3 - "$VOL" "$SNAP" "$OLD" "$STOPPED" "$EIP" "$NAT_EIP" "$VPC" "$IO1" "$S3IFACE" "$UNUSED_AMI" "$TPL_AMI" "$DHOST" "$KMSKEY" "$KMSON" "$NATGW" "$NATGW2" "$HC" "$HC2" "$IO1MA" "${EMPTYZONE##*/}" "${PRIVZONE##*/}" "${ZONE##*/}" "$MONITORED" "$PCA" <<'PY'
import json, sys
vol, snap, old, stopped, eip, nat_eip, vpc, io1, s3iface, unused_ami, tpl_ami, dhost, kmskey, kmson, natgw, natgw2, hc, hc2, io1ma, emptyzone, privzone, fullzone, monitored, pca = sys.argv[1:]
r = json.load(open(".e2e/report.json"))
f = r["findings"]
blob = json.dumps(f)
def has(check, needle):
    return any(x["check"] == check and needle in json.dumps(x) for x in f)
want = {
    "unattached-ebs": vol, "old-snapshots": snap, "old-instance-type": old,
    "stopped-instance": stopped, "idle-ip": eip, "log-retention": "/e2e/never-expires",
    "idle-load-balancer": "e2e-idle-alb", "nat-no-s3-endpoint": vpc, "gp2-to-gp3": "gp2",
    "piops-to-gp3": io1, "unused-secret": "e2e/never-read", "ecr-cleanup": "e2e-app",
    "lambda-x86": "e2e-x86", "s3-abort-mpu": "e2e-no-rules", "paid-s3-endpoint": s3iface, "old-cache-nodes": "e2e-old-cache", "dynamodb-no-autoscaling": "e2e-orders",
    "unused-ami": unused_ami, "idle-dedicated-host": dhost, "disabled-kms-key": kmskey, "rds-orphan-snapshot": "e2e-gone",
    "nat-no-routes": natgw, "idle-classic-lb": "e2e-clb", "s3-versions-no-expiry": "e2e-versioned", "r53-unused-health-check": hc,
    "io1-to-io2": io1ma, "api-cache-unused": "/stages/cold", "cloudtrail-duplicate": "e2e-trail-copy",
    "empty-hosted-zone": emptyzone, "ec2-detailed-monitoring": monitored, "idle-dms-instance": "e2e-dms-idle",
    "unused-private-ca": pca,
}
bad = [f"{c} missing {n}" for c, n in want.items() if not has(c, n)]
for never in (nat_eip, "/e2e/has-retention", "e2e-stage-variable-secret", "e2e-lambda-env-secret-value", "e2e-ecs-env-secret-value"):
    if never in blob:
        bad.append(f"false positive: {never}")
for check, never in (("unused-ami", tpl_ami), ("disabled-kms-key", kmson), ("empty-eks-cluster", "e2e-busy-eks"), ("nat-no-routes", natgw2), ("s3-versions-no-expiry", "e2e-versioned-ok"), ("r53-unused-health-check", hc2), ("empty-hosted-zone", privzone), ("empty-hosted-zone", fullzone), ("piops-to-gp3", io1ma), ("api-cache-unused", "/stages/warm"), ("cloudtrail-duplicate", '"resource": "e2e-all-regions"')):
    if has(check, never):
        bad.append(f"false positive: {check} {never}")
# EKS control-plane logs: a gone cluster's are left behind, a running one's are a plain never-expiring group.
def logs_of(group):
    return next((x for x in f if x["check"] == "log-retention" and x["resource"] == group), {})
gone_logs, live_logs = logs_of("/aws/eks/e2e-deleted-eks/cluster"), logs_of("/aws/eks/e2e-busy-eks/cluster")
if "no EKS cluster named e2e-deleted-eks" not in gone_logs.get("why", "") or "delete-log-group" not in " ".join(gone_logs.get("fix") or []):
    bad.append(f"a gone EKS cluster's log group must say left behind: {gone_logs}")
if not live_logs or "left behind" in live_logs.get("why", ""):
    bad.append(f"a running cluster's log group is a plain never-expiring one: {live_logs}")
# The run record: every call it made, all reads, none refused, and who ran it.
run = r.get("run") or {}
calls = run.get("calls", [])
ops = {c["operation"] for c in calls}
reads = ("Describe", "List", "Get", "Head", "BatchGet", "Lookup", "Search")
if not run.get("id") or len(ops) < 10 or not run.get("caller", "").startswith("arn:aws:"):
    bad.append(f"run record: id {run.get('id')!r}, {len(ops)} operations, caller {run.get('caller')!r}")
if "LookupEvents" in ops:
    bad.append("without -creators, CloudTrail's event history is never read")
if [c for c in calls if c["outcome"] == "refused"] or [o for o in ops if not o.startswith(reads) and o != "AssumeRole"]:
    bad.append(f"the run must make only read calls: {sorted(o for o in ops if not o.startswith(reads))}")
# Owners: the disk's tag comes with DescribeVolumes, the table's from tag:GetResources.
def owner(check, needle):
    x = next((x for x in f if x["check"] == check and needle in x["resource"]), {})
    return x.get("owner", ""), x.get("owner_from", "")
for (check, needle), expect in {("unattached-ebs", vol): ("payments", "tag Team"), ("dynamodb-no-autoscaling", "e2e-orders"): ("data-eng", "tag owner"),
                              ("piops-to-gp3", io1): ("", "none"), ("lambda-x86", "x86"): ("", "")}.items():
    if owner(check, needle) != expect:
        bad.append(f"owner of {check} {needle}: {owner(check, needle)}, want {expect}")
# moto doesn't implement DescribeFastSnapshotRestores (answers 500); real AWS does.
moto_gaps = {"ec2:DescribeFastSnapshotRestores", "ec2:DescribeImageReferences", "eks:ListCapabilities",  # moto has none of them
             "globalaccelerator:ListAccelerators",  # nor Global Accelerator
             "cloudwatch:DescribeAlarms", "route53:ListTrafficPolicyInstances",  # CBOR for CloudWatch; no traffic policies in moto
             "ec2:DescribeCapacityReservations",  # moto answers Capacity Reservation calls with a 500
             "kinesis:DescribeAccountSettings",  # moto lacks Kinesis account settings
             "dms:DescribeReplicationConfigs",  # nor DMS Serverless configurations (a 500)
             "fsx:DescribeVolumes",  # moto fsx has create/describe/delete file systems and backups, not volumes
             "fsx:DescribeDataRepositoryAssociations",  # moto fsx has no data repository associations
             "rolesanywhere:ListTrustAnchors"}  # moto has no IAM Roles Anywhere
# Without ListCapabilities the planted empty EKS cluster stays unknown here (unit tests cover it);
# the one with a node group must still stay quiet.
unexpected = [x for x in r["skipped"] if x["what"] not in moto_gaps]
if unexpected:
    bad.append(f"skipped: {unexpected}")
if bad:
    print("E2E FAIL:\n  " + "\n  ".join(bad)); sys.exit(1)
print(f"E2E PASS: all {len(want)} free checks found their planted waste, no false positives, total ${r['total_monthly_usd']:.2f}/mo")
PY

# rds-old-class needs a live database, but moto gives every database the same
# DbiResourceId, so a live one would make e2e-gone's snapshot look renamed
# (real IDs are unique): it gets a scan of its own and is removed again.
aws rds create-db-instance --db-instance-identifier e2e-old-class --db-instance-class db.m4.large --engine mysql --engine-version 8.0.36 --allocated-storage 20 --master-username e2e --master-user-password e2e-password-1 > /dev/null
aws rds create-db-instance --db-instance-identifier e2e-graviton --db-instance-class db.m5.large --engine mysql --engine-version 8.0.36 --allocated-storage 20 --master-username e2e --master-user-password e2e-password-1 > /dev/null
"$BIN" -regions $R -json -min-usd 0 > .e2e/report-old-class.json 2> /dev/null
aws rds delete-db-instance --db-instance-identifier e2e-old-class --skip-final-snapshot > /dev/null
aws rds delete-db-instance --db-instance-identifier e2e-graviton --skip-final-snapshot > /dev/null
python3 - <<'PY'
import json, sys
r = json.load(open(".e2e/report-old-class.json"))["findings"]
f = [x for x in r if x["check"] == "rds-old-class"]
if len(f) != 1 or f[0]["resource"] != "e2e-old-class" or not f[0].get("priced") or "db.m6g.large" not in f[0]["detail"]:
    print(f"E2E FAIL (rds-old-class): want e2e-old-class priced against db.m6g.large: {f}"); sys.exit(1)
g = [x for x in r if x["check"] == "rds-graviton"]
if len(g) != 1 or g[0]["resource"] != "e2e-graviton" or not g[0].get("priced") or "db.m6g.large" not in g[0]["detail"]:
    print(f"E2E FAIL (rds-graviton): want e2e-graviton priced against db.m6g.large: {g}"); sys.exit(1)
PY

# Acknowledgements and explain: ignoring the planted 100 GiB disk moves it
# out of the total, and explain finds a finding by its ID.
VOLID=$(python3 -c "import json,sys; r=json.load(open('.e2e/report.json')); print(next(f['id'] for f in r['findings'] if f['check']=='unattached-ebs' and sys.argv[1] in f['resource']))" "$VOL")
printf 'ignore:\n  - id: %s\n    reason: e2e restore drill\n    owner: e2e\n    until: 2099-01-01\n  - check: idle-nat\n    reason: matches nothing here\n' "$VOLID" > .e2e/ignore.yaml
"$BIN" -regions $R -json -ignore-file .e2e/ignore.yaml -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -secret-days 0 -min-usd 0 > .e2e/report-ack.json 2> /dev/null
"$BIN" explain "$VOLID" -from .e2e/report.json > .e2e/explain.txt
python3 - "$VOLID" <<'PY'
import json, sys
vid = sys.argv[1]
a, b = json.load(open(".e2e/report.json")), json.load(open(".e2e/report-ack.json"))
ex = open(".e2e/explain.txt").read()
bad = []
if not any(x["id"] == vid and x["ack_reason"] == "e2e restore drill" for x in b.get("acknowledged", [])):
    bad.append("the acknowledged disk is not listed as acknowledged")
if any(x["id"] == vid for x in b["findings"]):
    bad.append("the acknowledged disk is still a finding")
if abs((a["total_monthly_usd"] - b["total_monthly_usd"]) - 10.0) > 0.01:
    bad.append(f"total went {a['total_monthly_usd']} -> {b['total_monthly_usd']}, want 10.00 less")
if not any(u.get("check") == "idle-nat" for u in b.get("unused_rules", [])):
    bad.append("the rule matching nothing is not reported")
if vid not in ex or "Read with (read-only calls): ec2:DescribeVolumes" not in ex:
    bad.append("explain output incomplete")
if bad:
    print("E2E FAIL (acknowledge/explain):\n  " + "\n  ".join(bad)); sys.exit(1)
print(f"E2E PASS (acknowledge/explain): {vid} acknowledged, total down $10.00, unused rule reported, explain works")
PY

# Paid plans, when the seller's signing key is on this machine: a Team key for
# moto's account, a 0-day usage window (moto resources are brand new), and the
# HTML report.
SIGNKEY=${COSTHOUND_SIGNING_KEY:-$HOME/.config/costhound-seller/signing.key}
if [ -f "$SIGNKEY" ]; then
  go build -tags pro -o bin/costhound-license ./cmd/costhound-license
  KEY=$(bin/costhound-license issue -key "$SIGNKEY" -plan team -accounts 123456789012 -id e2e-team)
  HIST=$(mktemp -d)
  # Terraform state that manages the planted orders table, with a secret costhound must never repeat.
  mkdir -p .e2e/tf
  printf '%s\n' '{"version": 4, "resources": [{"mode": "managed", "type": "aws_dynamodb_table", "name": "orders", "instances": [{"attributes": {"id": "e2e-orders", "arn": "arn:aws:dynamodb:eu-west-1:123456789012:table/e2e-orders", "e2e_secret": "e2e-tf-secret-value"}}]}]}' > .e2e/tf/prod.tfstate
  "$BIN" -regions $R -license "$KEY" -usage-days 0 -json -html .e2e/report.html -ics .e2e/cliffs.ics -tf-state .e2e/tf -history-dir "$HIST" -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/report-team.json 2> .e2e/team.err
  rm -f .e2e/redact.key .e2e/redact-map.json .e2e/report-full.json
  "$BIN" -regions $R -license "$KEY" -usage-days 0 -json -redact -redact-full .e2e/report-full.json -redact-key .e2e/redact.key -redact-map .e2e/redact-map.json -no-history \
    -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/report-redacted.json 2> .e2e/redact.err || true # a refusal is reported below
  # Signed: a key of our own, a signed redacted copy, verify it, then a tampered copy must fail.
  rm -f .e2e/keys/report-signing.key .e2e/keys/report-signing.pub .e2e/signed.json .e2e/signed.json.sig
  "$BIN" keygen -dir .e2e/keys > .e2e/keygen.out 2>&1 || true
  "$BIN" -regions $R -license "$KEY" -usage-days 0 -no-history -redact -redact-key .e2e/redact.key -sign .e2e/signed.json -sign-key .e2e/keys/report-signing.key \
    -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > /dev/null 2> .e2e/sign.err || true
  "$BIN" verify -key .e2e/keys/report-signing.pub .e2e/signed.json > .e2e/verify-ok.out 2>&1 && echo 0 > .e2e/verify-ok.code || echo $? > .e2e/verify-ok.code
  sed 's/"total_monthly_usd": *[0-9.]*/"total_monthly_usd": 1/' .e2e/signed.json > .e2e/tampered.json && cp .e2e/signed.json.sig .e2e/tampered.json.sig
  "$BIN" verify -key .e2e/keys/report-signing.pub .e2e/tampered.json > .e2e/verify-bad.out 2>&1 && echo 0 > .e2e/verify-bad.code || echo $? > .e2e/verify-bad.code
  python3 - "$TRANSFER" "$DMS" <<'PY'
import hashlib, json, os, sys
transfer, dms = sys.argv[1:]
r = json.load(open(".e2e/report-team.json"))
checks = {f["check"] for f in r["findings"]}
bad = []
if r["plan"] != "team":
    bad.append(f"plan is {r['plan']}")
if any(f["check"] == "idle-network-firewall" for f in r["findings"]):
    bad.append("idle-network-firewall judged a firewall it can't see the endpoints or metrics of")
if not any(f["check"] == "opensearch-extended-support" and f["resource"] == "e2e-old-search" for f in r["findings"]):
    bad.append("opensearch-extended-support missed the planted Elasticsearch 7.4 domain")
# moto can't answer CloudWatch's CBOR protocol (the Go SDK's default since
# 2025), so usage-based checks are proven by scripts/real-aws-smoke.sh instead.
known = {"cloudwatch:GetMetricData", "cloudwatch:ListMetrics", "cost-optimization-hub:ListRecommendations", "pricing:GetProducts", "ec2:DescribeFastSnapshotRestores",
         "ec2:DescribeImageReferences", "eks:DescribeClusterVersions", "eks:ListCapabilities", "ec2:DescribeClientVpnEndpoints",
         "network-firewall:ListFirewalls", "cloudwatch:DescribeAlarms", "route53:ListTrafficPolicyInstances", "ec2:DescribeCapacityReservations",
         "lambda:ListProvisionedConcurrencyConfigs",  # moto: 404
         "aoss:ListCollectionGroups",  # moto: 500 (no collection groups)
         "dms:DescribeReplicationConfigs",  # no DMS Serverless configurations
         "dms:DescribeReplications",  # no DMS Serverless runtime state
         "transfer:ListUsers",  # Moto can create users but cannot list them
         "kinesis:DescribeAccountSettings",  # moto lacks Kinesis account settings
         "fsx:DescribeVolumes",  # moto fsx has create/describe/delete file systems and backups, not volumes
         "fsx:DescribeDataRepositoryAssociations",  # moto fsx has no data repository associations
         "rolesanywhere:ListTrustAnchors",  # moto has no IAM Roles Anywhere
         # no reservations in moto (Team: commitments-expiring)
         "ec2:DescribeReservedInstances", "rds:DescribeReservedDBInstances", "elasticache:DescribeReservedCacheNodes",
         "es:DescribeReservedInstances", "redshift:DescribeReservedNodes", "savingsplans:DescribeSavingsPlans",
         # the Go SDK speaks CBOR to MemoryDB too (since v1.43), which moto can't answer
         "memorydb:DescribeClusters",
         # moto has no Global Accelerator
         "globalaccelerator:ListAccelerators"}  # the Go SDK speaks CBOR to Network Firewall too, which moto can't answer  # moto has no Cost Optimization Hub, Price List, AMI references or EKS version table and capabilities
other = [x for x in r["skipped"] if x["what"] not in known]
if other:
    bad.append(f"unexpected skips: {other}")
calls = r.get("run", {}).get("calls", [])
read_ok = {c["operation"] for c in calls if c["outcome"] == "ok"}
for op in ("ListServers", "DescribeServer", "ListResolverEndpoints", "DescribeReplicationInstances", "DescribeReplicationTasks", "DescribeFileSystems"):
    if op not in read_ok:
        bad.append(f"expected the SDK inventory read {op} to succeed")
if not any(c["operation"] == "ListUsers" for c in calls):
    bad.append("expected a user inventory read for the stopped Transfer server")
if not os.path.getsize(".e2e/report.html") > 10000:
    bad.append("HTML report missing or tiny")
tf = next((f for f in r["findings"] if f["check"] == "dynamodb-no-autoscaling" and f["resource"] == "e2e-orders"), {})
if tf.get("tf_address") != "aws_dynamodb_table.orders" or not tf.get("fix", [""])[0].startswith("# managed by Terraform"):
    bad.append(f"-tf-state: the orders table must be marked as managed: {tf.get('tf_address')!r} {tf.get('fix', [])[:1]}")
if any(b"e2e-tf-secret-value" in open(p, "rb").read() for p in (".e2e/report-team.json", ".e2e/report.html", ".e2e/team.err")):
    bad.append("-tf-state: a value from the state file leaked into the output")
if any(b"e2e-stage-variable-secret" in open(p, "rb").read() for p in (".e2e/report-team.json", ".e2e/report.html", ".e2e/team.err")):
    bad.append("an API Gateway stage variable leaked into the output (costhound must never keep them)")
if any(b"e2e-ecs-env-secret-value" in open(p, "rb").read() for p in (".e2e/report-team.json", ".e2e/report.html", ".e2e/team.err")):
    bad.append("an ECS container's environment leaked into the output (costhound never keeps container definitions)")
if any(b"e2e-lambda-env-secret-value" in open(p, "rb").read() for p in (".e2e/report-team.json", ".e2e/report.html", ".e2e/team.err")):
    bad.append("a Lambda environment variable leaked into the output (costhound never keeps them)")
for secret in (b"e2e-hive-secret-value", b"e2e-step-secret-value", b"e2e-kdc-secret-value"):
    if any(secret in open(p, "rb").read() for p in (".e2e/report-team.json", ".e2e/report.html", ".e2e/team.err")):
        bad.append(f"an EMR secret leaked into the output: {secret.decode()} (configurations, steps and Kerberos settings are never kept)")
ics = open(".e2e/cliffs.ics", "rb").read() if os.path.exists(".e2e/cliffs.ics") else b""
if not ics.startswith(b"BEGIN:VCALENDAR\r\n") or not ics.endswith(b"END:VCALENDAR\r\n") or ics.count(b"BEGIN:VEVENT") < len(r.get("upcoming") or []):
    bad.append(f"-ics: not a calendar of the {len(r.get('upcoming') or [])} upcoming cliff(s): {ics[:80]!r}")
# -redact: no planted name or ID may survive, the tokens map back,
# and the commitment matches the full report's bytes.
import re
red = open(".e2e/report-redacted.json").read()
rj = json.loads(red) if red.strip() else {}
full_bytes = open(".e2e/report-full.json", "rb").read() if os.path.exists(".e2e/report-full.json") else b""
# Every planted name or ID the full report shows: in resources, and in facts
# (which also name what a summing-up finding's text lists).
plain = {"window_days", "max_bytes", "state", "engine", "node_type", "nodes", "instance_type", "instances", "brokers",
         "has_lifecycle", "has_rules", "replication_group", "type", "from_type", "size_gib", "lb_type", "version", "class"}
idre = re.compile(r"^[a-z]+(-[a-z]+)*-[0-9a-f]{8,17}$")
# Also check the planted identities when unsupported Moto reads leave their
# Pro findings unknown. The unredacted Free report above checks the DMS name.
names = {"123456789012", transfer, dms, "e2e-dms-idle", "e2e-transfer-user"}
for f in r["findings"]:
    words = [f["resource"].split(" ")[0]] + [w for k, v in (f.get("facts") or {}).items() if k not in plain for w in v.split()]
    names |= {w for w in words if w.startswith(("e2e", "/e2e")) or idre.match(w)}
# And every planted name the full report's own wording shows, fix commands
# and facts aside (the copy drops them): a name that is only in prose is one
# the redactor can't collect, so it must be caught here, not trusted.
prose = re.compile(r"(?<![A-Za-z0-9_-])/?e2e(?:[A-Za-z0-9_-]+|(?=/[A-Za-z0-9_-]))(?:/[A-Za-z0-9_-]+)*")
for f in r["findings"] + (r.get("upcoming") or []):
    names |= set(prose.findall(json.dumps({k: v for k, v in f.items() if k not in ("fix", "facts")})))
leaked = sorted(n for n in names if n in red)
if not rj or leaked or not rj.get("redaction", {}).get("commitment") or any(f.get("fix") or f.get("facts") for f in rj["findings"]):
    bad.append(f"redacted report: leaked {leaked[:5]}, commitment {rj.get('redaction')}, {open('.e2e/redact.err').read()[-300:]}")
elif not full_bytes or hashlib.sha256(full_bytes).hexdigest() != rj["redaction"]["commitment"]:
    bad.append("the redacted copy's commitment must match the full report file")
else:
    full = json.loads(full_bytes)
    if len(rj["findings"]) != len(full["findings"]) or rj["total_monthly_usd"] != full["total_monthly_usd"]:
        bad.append("the redacted copy must keep every finding and the total")
    if "123456789012" not in json.load(open(".e2e/redact-map.json")).values():
        bad.append("the redaction map must lead back to the account")
# Signed reports: the signed copy verifies; a changed one doesn't.
ok_code, bad_code = open(".e2e/verify-ok.code").read().strip(), open(".e2e/verify-bad.code").read().strip()
if "fingerprint" not in open(".e2e/keygen.out").read():
    bad.append(f"keygen: {open('.e2e/keygen.out').read()[-300:]}")
elif ok_code != "0" or "OK:" not in open(".e2e/verify-ok.out").read() or "redacted copy" not in open(".e2e/verify-ok.out").read():
    bad.append(f"verify of the signed copy: exit {ok_code}: {open('.e2e/verify-ok.out').read()[-400:]} {open('.e2e/sign.err').read()[-300:]}")
elif bad_code != "1" or "has changed since it was signed" not in open(".e2e/verify-bad.out").read():
    bad.append(f"verify of a changed copy: exit {bad_code}: {open('.e2e/verify-bad.out').read()[-300:]}")
skipped = sorted({s["what"] + " " + s["error"] for s in r["skipped"]})
if bad:
    print("E2E FAIL (team):\n  " + "\n  ".join(bad)); sys.exit(1)
print(f"E2E PASS (team): {len(r['findings'])} findings, HTML report written, redacted copy leaks none of {len(names)} names, signed copy verifies and a changed one fails; known moto gaps skipped: {skipped or 'nothing'}")
PY

  # Savings ledger: delete the planted disk, scan again with the same
  # thresholds, and the ledger must show one verified $10/mo fix.
  aws ec2 delete-volume --volume-id "$VOL"
  "$BIN" -regions $R -license "$KEY" -usage-days 0 -json -history-dir "$HIST" -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -min-usd 0 > .e2e/report-team2.json 2> /dev/null
  python3 - <<'PY'
import json, sys
l = json.load(open(".e2e/report-team2.json")).get("ledger") or {}
if l.get("this_run_fixes") != 1 or abs(l.get("this_run_monthly_usd", 0) - 10.0) > 0.01 or l.get("fixes") != 1:
    print(f"E2E FAIL (ledger): {l}"); sys.exit(1)
print(f"E2E PASS (ledger): deleting the planted disk = 1 verified fix, ${l['monthly_usd_now']:.2f}/mo no longer spent")
PY

  # aurora-serverless-pause: an Aurora Serverless v2 cluster gets a scan of
  # its own (moto gives every database the same DbiResourceId, which would
  # make e2e-gone's snapshot look renamed). Its hours come from CloudWatch,
  # which moto can't serve (CBOR), so it stays unknown here; the loader must
  # still find its Serverless v2 instance (it then asks for the engine
  # version, which moto answers with a 500) and judge nothing.
  #
  # idle-opensearch-serverless rides along: a vector collection outside
  # groups. moto lists and describes collections, but has no collection
  # groups (a 500) and no CloudWatch, so the collection must stay unjudged:
  # no finding, and the group read (made after the collections) skipped.
  PROKEY=$(bin/costhound-license issue -key "$SIGNKEY" -plan pro -accounts 123456789012 -id e2e-pro-aurora)
  # (moto needs a KmsARN in the encryption policy: it has no AWS owned key)
  aws opensearchserverless create-security-policy --name e2e-kb-enc --type encryption --policy '{"Rules":[{"ResourceType":"collection","Resource":["collection/e2e-kb"]}],"AWSOwnedKey":false,"KmsARN":"arn:aws:kms:us-east-1:123456789012:key/e2e-kb-key"}' > /dev/null
  AOSSID=$(aws opensearchserverless create-collection --name e2e-kb --type VECTORSEARCH --query createCollectionDetail.id --output text)
  aws rds create-db-cluster --db-cluster-identifier e2e-aurora --engine aurora-mysql --engine-version 8.0.mysql_aurora.3.08.0 --master-username e2e --master-user-password e2e-password-1 --serverless-v2-scaling-configuration MinCapacity=0.5,MaxCapacity=4 > /dev/null
  aws rds create-db-instance --db-instance-identifier e2e-aurora-1 --db-cluster-identifier e2e-aurora --engine aurora-mysql --db-instance-class db.serverless > /dev/null
  "$BIN" -regions $R -license "$PROKEY" -usage-days 0 -json -no-history -min-usd 0 > .e2e/report-aurora.json 2> /dev/null
  aws rds delete-db-instance --db-instance-identifier e2e-aurora-1 --skip-final-snapshot > /dev/null
  aws rds delete-db-cluster --db-cluster-identifier e2e-aurora --skip-final-snapshot > /dev/null
  aws opensearchserverless delete-collection --id "$AOSSID" > /dev/null
  python3 - <<'PY'
import json, sys
r = json.load(open(".e2e/report-aurora.json"))
what = {s["what"] for s in r["skipped"]}
bad = []
if r["plan"] != "pro":
    bad.append(f"plan is {r['plan']}")
if "rds:DescribeDBEngineVersions" not in what:
    bad.append(f"the Serverless v2 instance wasn't found (no engine-version read): {sorted(what)}")
if any(f["check"] == "aurora-serverless-pause" for f in r["findings"]):
    bad.append("aurora-serverless-pause judged a cluster whose hours it couldn't read")
if "aoss:ListCollectionGroups" not in what or what & {"aoss:ListCollections", "aoss:BatchGetCollection"}:
    bad.append(f"the OpenSearch Serverless collection wasn't listed and described before the group read: {sorted(what)}")
if any(f["check"] == "idle-opensearch-serverless" for f in r["findings"]):
    bad.append("idle-opensearch-serverless judged a collection whose requests it couldn't read")
if bad:
    print("E2E FAIL (aurora):\n  " + "\n  ".join(bad)); sys.exit(1)
print("E2E PASS (aurora): the Serverless v2 cluster's instance and the OpenSearch Serverless collection found, nothing judged without their metrics")
PY

  # Team automation: the summary goes to a local fake webhook, and the CI gate
  # must exit 3 when the waste is over the limit.
  HOOKPORT=$((PORT + 1))
  python3 - "$HOOKPORT" > .e2e/hook.json <<'PY' &
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        self.send_response(200); self.end_headers()
        sys.stdout.write(body.decode()); sys.stdout.flush()
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).handle_request()
PY
  HOOK=$!
  sleep 0.5
  set +e
  "$BIN" -regions $R -license "$KEY" -no-history -slack-webhook "http://127.0.0.1:$HOOKPORT/hook" -fail-over 1 -min-usd 0 > /dev/null 2> .e2e/gate.err
  GATE=$?
  "$BIN" -regions $R -license "$KEY" -no-history -fail-on-unowned 1 -min-usd 0 > /dev/null 2> .e2e/gate-unowned.err
  GATE2=$?
  # -fail-scope: the state in .e2e/tf manages only the orders table (unpriced, owned), so a
  # Terraform-scoped gate passes and one scoped outside it fails; without state it's refused.
  "$BIN" -regions $R -license "$KEY" -no-history -tf-state .e2e/tf -fail-scope terraform -fail-over 1 -fail-on-unowned 1 -min-usd 0 > /dev/null 2> .e2e/gate-tf.err
  GATE3=$?
  "$BIN" -regions $R -license "$KEY" -no-history -tf-state .e2e/tf -fail-scope outside -fail-over 1 -min-usd 0 > /dev/null 2> .e2e/gate-out.err
  GATE4=$?
  "$BIN" -regions $R -license "$KEY" -no-history -fail-scope outside -fail-over 1 -min-usd 0 > /dev/null 2> .e2e/gate-notf.err
  GATE5=$?
  set -e
  wait $HOOK 2> /dev/null || true
  python3 - "$GATE" "$GATE2" "$GATE3" "$GATE4" "$GATE5" <<'PY'
import json, sys
gate, gate2, gate3, gate4, gate5 = (int(a) for a in sys.argv[1:6])
msg = json.load(open(".e2e/hook.json"))["text"]
bad = []
if gate != 3:
    bad.append(f"-fail-over exit code {gate}, want 3")
if "Estimated waste" not in msg or "123456789012" not in msg:
    bad.append(f"webhook message: {msg!r}")
if "Unowned:" not in msg or "By owner: " not in msg or "data-eng" not in msg:  # the payments disk is gone by now (ledger part)
    bad.append(f"the webhook message must lead with unowned waste and owners: {msg!r}")
if gate2 != 3 or "no owner tag is over the -fail-on-unowned limit" not in open(".e2e/gate-unowned.err").read():
    bad.append(f"-fail-on-unowned exit code {gate2}, want 3: {open('.e2e/gate-unowned.err').read()[-500:]}")
if gate3 != 0:
    bad.append(f"-fail-scope terraform: only the unpriced, owned orders table is managed, exit {gate3}, want 0: {open('.e2e/gate-tf.err').read()[-500:]}")
if gate4 != 3 or "outside the Terraform state given" not in open(".e2e/gate-out.err").read():
    bad.append(f"-fail-scope outside: exit {gate4}, want 3: {open('.e2e/gate-out.err').read()[-500:]}")
if gate5 != 2 or "needs -tf-state" not in open(".e2e/gate-notf.err").read():
    bad.append(f"-fail-scope without -tf-state: exit {gate5}, want 2: {open('.e2e/gate-notf.err').read()[-300:]}")
if bad:
    print("E2E FAIL (team automation):\n  " + "\n  ".join(bad)); sys.exit(1)
print("E2E PASS (team automation): summary with owners posted to the webhook, CI gates (-fail-over, -fail-on-unowned) exited 3, -fail-scope kept them to Terraform or outside it")
PY

  # Organization scan: a member account with its own waste, reached through
  # an assumed role, merged into one report.
  ORG=$(q organizations create-organization --feature-set ALL --query Organization.Id)
  MEMBER=$(q organizations create-account --email e2e-member@example.com --account-name e2e-member --query CreateAccountStatus.AccountId)
  read -r AK SK ST <<< "$(q sts assume-role --role-arn "arn:aws:iam::$MEMBER:role/CosthoundReadOnly" --role-session-name e2e --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]')"
  MVOL=$(AWS_ACCESS_KEY_ID=$AK AWS_SECRET_ACCESS_KEY=$SK AWS_SESSION_TOKEN=$ST q ec2 create-volume --availability-zone ${R}a --size 50 --volume-type gp3 --query VolumeId)
  ORGKEY=$(bin/costhound-license issue -key "$SIGNKEY" -plan team -org "$ORG" -id e2e-org)
  "$BIN" -org -regions $R -license "$ORGKEY" -no-history -json -html .e2e/report-org.html -snapshot-age 0 -stopped-days 0 -min-log-gib 0 -secret-days 0 -min-usd 0 > .e2e/report-org.json 2> .e2e/org.err
  python3 - "$MEMBER" "$MVOL" <<'PY'
import json, sys
member, mvol = sys.argv[1:]
r = json.load(open(".e2e/report-org.json"))
ids = {a["id"]: a for a in r.get("accounts", [])}
bad = []
if set(ids) != {"123456789012", member}:
    bad.append(f"accounts = {list(ids)}")
if not any(f.get("account") == member and mvol in f["resource"] and f["check"] == "unattached-ebs" for f in r["findings"]):
    bad.append(f"member volume {mvol} not reported under account {member}")
if any(a.get("error") for a in ids.values()):
    bad.append(f"account errors: {[a for a in ids.values() if a.get('error')]}")
if abs(sum(a["total_monthly_usd"] for a in ids.values()) - r["total_monthly_usd"]) > 0.01:
    bad.append("account totals don't add up to the total")
if bad:
    print("E2E FAIL (organization):\n  " + "\n  ".join(bad)); sys.exit(1)
print(f"E2E PASS (organization): {len(ids)} accounts merged, member's volume found under {member}, total ${r['total_monthly_usd']:.2f}/mo")
PY

  # Fix bundles: costhound plan, then the generated scripts run against moto
  # (the open-source tree has no fix bundles, nor this part).
  if [ -f scripts/e2e-bundle.sh ]; then
    . scripts/e2e-bundle.sh
  fi
fi
