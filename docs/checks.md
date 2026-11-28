| Cloud | ID | Plan | What it finds | Pricing | Fix script |
|---|---|---|---|---|---|
| aws | `api-cache-unused` | free | API Gateway caches no method uses | live list price | no |
| aws | `backup-orphans` | free | Backups of resources that no longer exist | cost-only | no |
| aws | `cloudtrail-duplicate` | free | CloudTrail trails delivering a second copy of management events | unpriced | no |
| aws | `disabled-kms-key` | free | KMS keys switched off but still billed | live list price | no |
| aws | `dynamodb-no-autoscaling` | free | DynamoDB tables paying for their peak all day | cost-only | yes |
| aws | `ec2-detailed-monitoring` | free | EC2 detailed monitoring nobody needs | live list price | yes |
| aws | `ec2-graviton` | free | EC2 instances that could run on Graviton | live list price | no |
| aws | `ecr-cleanup` | free | Container images piling up in ECR | live list price | yes |
| aws | `efs-no-mount-targets` | free | EFS file systems nothing can reach | live list price | no |
| aws | `empty-eks-cluster` | free | EKS clusters with nothing to run on | live list price | no |
| aws | `empty-hosted-zone` | free | Route 53 hosted zones with no records | live list price | no |
| aws | `fast-snapshot-restore` | free | Snapshots with Fast Snapshot Restore left on | live list price | yes |
| aws | `gp2-to-gp3` | free | gp2 volumes that should be gp3 | live list price | yes |
| aws | `idle-accelerator` | free | Global Accelerators that route nothing | live list price | no |
| aws | `idle-classic-lb` | free | Classic Load Balancers with no instance in service | live list price | no |
| aws | `idle-dedicated-host` | free | Dedicated Hosts running no instances | live list price | yes |
| aws | `idle-dms-instance` | free | Idle DMS replication instances and Serverless replications | live list price | no |
| aws | `idle-ip` | free | Public IPs doing nothing | live list price | yes |
| aws | `idle-load-balancer` | free | Load balancers with no targets | live list price | yes |
| aws | `io1-to-io2` | free | Multi-Attach io1 volumes that cost less as io2 | live list price | yes |
| aws | `lambda-x86` | free | Lambda functions on x86 instead of arm64 | unpriced | no |
| aws | `log-retention` | free | Log groups that never expire | live list price | yes |
| aws | `nat-no-routes` | free | NAT gateways no route uses | live list price | yes |
| aws | `nat-no-s3-endpoint` | free | VPCs with a NAT gateway and no S3 gateway endpoint | unpriced | yes |
| aws | `old-cache-nodes` | free | ElastiCache on old node types | unpriced | no |
| aws | `old-instance-type` | free | Old-generation instance types | unpriced | no |
| aws | `old-snapshots` | free | Old snapshots nobody uses | live list price | yes |
| aws | `orphan-alarm` | free | CloudWatch alarms on resources that are gone | live list price | yes |
| aws | `paid-s3-endpoint` | free | Paid S3/DynamoDB endpoints where a free one works | live list price | yes |
| aws | `piops-to-gp3` | free | io1/io2 volumes that fit gp3 | live list price | yes |
| aws | `r53-unused-health-check` | free | Route 53 health checks nothing uses | live list price | no |
| aws | `rds-graviton` | free | RDS databases that could run on Graviton | live list price | no |
| aws | `rds-old-class` | free | RDS databases on previous-generation classes | live list price | no |
| aws | `rds-orphan-snapshot` | free | RDS snapshots of deleted databases | live list price | no |
| aws | `s3-abort-mpu` | free | Buckets that keep failed uploads forever | unpriced | yes |
| aws | `s3-versions-no-expiry` | free | Versioned buckets that keep every old version | unpriced | no |
| aws | `stopped-instance` | free | Stopped instances still paying for disks | live list price | yes |
| aws | `unattached-ebs` | free | Unattached EBS volumes | live list price | yes |
| aws | `unused-ami` | free | AMIs nobody launches | live list price | no |
| aws | `unused-capacity-reservation` | free | EC2 Capacity Reservations nobody uses | live list price | no |
| aws | `unused-private-ca` | free | Private CAs billed while they can't issue | live list price | no |
| aws | `unused-secret` | free | Secrets nobody reads | live list price | yes |
| aws | `unused-workspace` | free | WorkSpaces nobody signed in to for 30 days | live list price | no |
| aws | `aurora-serverless-pause` | pro | Aurora Serverless v2 clusters that could pause when idle | live list price | no |
| aws | `aurora-storage-config` | pro | Aurora clusters on the costlier storage configuration | live list price | no |
| aws | `burstable-surplus-credits` | pro | Burstable EC2 instances paying surplus CPU credits | live list price | no |
| aws | `cloudfront-dedicated-ip-ssl` | pro | CloudFront custom certificates using dedicated-IP SSL | live list price | no |
| aws | `commitment-underused` | pro | Savings Plans and Reserved Instances paid for but not used | Cost Explorer (your bill) | no |
| aws | `comprehend-idle-endpoint` | pro | Comprehend real-time endpoints with no requests | live list price | no |
| aws | `compute-optimizer` | pro | AWS Compute Optimizer rightsizing (when the hub is off) | live list price | no |
| aws | `cost-hub` | pro | AWS Cost Optimization Hub: rightsize, idle, Graviton | live list price | no |
| aws | `custom-metrics` | pro | CloudWatch custom metrics: where the monitoring bill comes from | cost-only | no |
| aws | `dynamodb-autoscaling-floor` | pro | DynamoDB auto scaling with a minimum far above use | live list price | no |
| aws | `dynamodb-stale-ondemand` | pro | On-demand DynamoDB tables unused for 90 days | live list price | no |
| aws | `dynamodb-table-class` | pro | DynamoDB tables on the costlier table class | live list price | no |
| aws | `ebs-provisioned-performance` | pro | EBS volumes with excess provisioned IOPS or throughput | live list price | no |
| aws | `ec2-sqlserver-overprovisioned` | pro | EC2 instances overprovisioned for SQL Server | live list price | no |
| aws | `ecs-cluster-overprovisioned` | pro | ECS clusters with excess EC2 container instances (preview) | live list price | no |
| aws | `efs-provisioned-throughput` | pro | EFS provisioned throughput above measured need | live list price | no |
| aws | `eks-extended-support` | pro | EKS clusters paying for extended support | live list price | no |
| aws | `eks-overrequested` | pro | EKS clusters whose nodes are held by requests pods don't use | live list price | no |
| aws | `elasticache-extended-support` | pro | ElastiCache Redis OSS paying for extended support | live list price | no |
| aws | `elasticache-oversized` | pro | ElastiCache clusters a node size bigger than their peak load needs | live list price | no |
| aws | `elasticache-redis-to-valkey` | pro | ElastiCache Redis OSS caches that could run Valkey on the same nodes for less | live list price | no |
| aws | `emr-no-auto-termination` | pro | EMR clusters with no auto-termination policy | unpriced | no |
| aws | `emr-prev-gen-instances` | pro | EMR clusters running previous-generation instances | live list price | no |
| aws | `fargate-oversized` | pro | Fargate services sized bigger than their busiest task needs | live list price | no |
| aws | `fargate-x86` | pro | Fargate services on x86 that could run on Graviton | live list price | no |
| aws | `idle-apprunner-service` | pro | App Runner services with no requests | live list price | no |
| aws | `idle-appstream-fleet` | pro | AppStream fleets with no sessions | live list price | no |
| aws | `idle-attached-ebs` | pro | Disks attached to running instances with no reads or writes | live list price | no |
| aws | `idle-bedrock-provisioned-throughput` | pro | Bedrock Provisioned Throughput with no invocations | live list price | no |
| aws | `idle-client-vpn` | pro | Client VPN endpoints nobody connects to | live list price | no |
| aws | `idle-cloudhsm` | pro | Idle CloudHSM cluster | live list price | no |
| aws | `idle-dax` | pro | DAX clusters with no requests | live list price | no |
| aws | `idle-db-cluster` | pro | Aurora, Multi-AZ, DocumentDB and Neptune DB clusters nobody connects to | live list price | no |
| aws | `idle-docdb-elastic` | pro | DocumentDB elastic clusters with no connections or operations | live list price | no |
| aws | `idle-dx-connection` | pro | Direct Connect connections that carry nothing or are down | live list price | no |
| aws | `idle-dynamodb` | pro | DynamoDB tables with provisioned capacity and no traffic | live list price | yes |
| aws | `idle-dynamodb-index` | pro | DynamoDB indexes nobody reads | live list price | no |
| aws | `idle-ec2` | pro | EC2 instances idle every day | live list price | no |
| aws | `idle-ecs-service` | pro | ECS services with running tasks and no load balancer traffic | live list price | no |
| aws | `idle-efs` | pro | EFS file systems nobody connects to | live list price | no |
| aws | `idle-elasticache` | pro | ElastiCache clusters nobody reads or writes | live list price | no |
| aws | `idle-emr` | pro | EMR clusters that did no work | live list price | no |
| aws | `idle-emr-serverless-app` | pro | EMR Serverless applications paying for warm workers with no job runs | live list price | no |
| aws | `idle-endpoint` | pro | Interface and Gateway Load Balancer endpoints with no traffic | live list price | yes |
| aws | `idle-fsx` | pro | FSx file systems nobody reads or writes | live list price | no |
| aws | `idle-glue-session` | pro | Glue interactive sessions and development endpoints left READY | live list price | no |
| aws | `idle-kendra-index` | pro | Kendra indexes with no queries | live list price | no |
| aws | `idle-kinesis` | pro | Kinesis streams with no records | live list price | yes |
| aws | `idle-memorydb` | pro | MemoryDB clusters nobody reads or writes | live list price | no |
| aws | `idle-mq` | pro | Amazon MQ brokers with no messaging activity | live list price | no |
| aws | `idle-msk` | pro | MSK clusters with no messages | live list price | no |
| aws | `idle-mwaa` | pro | Idle Amazon MWAA environment | live list price | no |
| aws | `idle-nat` | pro | NAT gateways with (almost) no traffic | live list price | yes |
| aws | `idle-neptune-graph` | pro | Neptune Analytics graphs with no queries | live list price | no |
| aws | `idle-network-firewall` | pro | Network Firewalls with no traffic | live list price | no |
| aws | `idle-opensearch` | pro | OpenSearch domains nobody searches or writes to | live list price | no |
| aws | `idle-opensearch-serverless` | pro | OpenSearch Serverless collection groups and collections nobody uses | live list price | no |
| aws | `idle-osi-pipeline` | pro | OpenSearch Ingestion pipelines with no incoming records | live list price | no |
| aws | `idle-provisioned-concurrency` | pro | Lambda provisioned concurrency nobody uses | live list price | no |
| aws | `idle-rds` | pro | RDS databases nobody connects to | live list price | no |
| aws | `idle-rds-proxy` | pro | RDS Proxies with no client connections | live list price | no |
| aws | `idle-rds-replica` | pro | RDS read replicas nobody reads from | live list price | no |
| aws | `idle-redshift` | pro | Redshift clusters nobody connects to | live list price | yes |
| aws | `idle-resolver-endpoint` | pro | Route 53 Resolver endpoints with no queries | live list price | no |
| aws | `idle-sagemaker-endpoint` | pro | SageMaker endpoints with no invocations | live list price | yes |
| aws | `idle-sagemaker-notebook` | pro | SageMaker notebook instances to review | live list price | no |
| aws | `idle-tgw-attachment` | pro | Transit Gateway attachments with no traffic | live list price | no |
| aws | `idle-transfer-server` | pro | Stopped or idle Transfer Family servers | live list price | no |
| aws | `keyspaces-provisioned-unused` | pro | Keyspaces tables with underused provisioned throughput | live list price | no |
| aws | `lambda-errors` | pro | Lambda functions whose calls keep failing | unpriced | no |
| aws | `lambda-timeouts` | pro | Lambda functions frequently reaching their timeout | cost-only | no |
| aws | `lightsail-waste` | pro | Stopped or unattached Lightsail resources and load balancers with no instances | live list price | no |
| aws | `log-ingest` | pro | Log groups that ingest the most | live list price | no |
| aws | `opensearch-extended-support` | pro | OpenSearch and Elasticsearch domains past standard support | live list price | no |
| aws | `opensearch-oversized` | pro | OpenSearch data nodes with room to downsize | live list price | no |
| aws | `public-ipv4-in-use` | pro | Public IPv4 on resources that may not need it | live list price | no |
| aws | `quiet-accelerator` | pro | Global Accelerators with no traffic | live list price | no |
| aws | `quiet-api-cache` | pro | API Gateway caches nothing uses | live list price | no |
| aws | `quiet-classic-lb` | pro | Classic Load Balancers with no traffic | live list price | no |
| aws | `quiet-load-balancer` | pro | Load balancers with targets but no traffic | live list price | yes |
| aws | `rds-extended-support` | pro | RDS and Aurora on engine versions past standard support | live list price | no |
| aws | `rds-multiaz-nonprod` | pro | Multi-AZ RDS for non-production tags | live list price | no |
| aws | `rds-oversized` | pro | RDS databases a class size bigger than their peak load needs | live list price | no |
| aws | `rds-piops-to-gp3` | pro | RDS io1 storage that costs less as gp3 | live list price | no |
| aws | `redshift-orphan-snapshot` | pro | Redshift manual snapshots of deleted clusters | live list price | no |
| aws | `redshift-oversized` | pro | Provisioned Redshift clusters with room to downsize | live list price | no |
| aws | `s3-standard-no-tiering` | pro | Large S3 Standard buckets without tiering | cost-only | no |
| aws | `spot-candidate-nonprod` | pro | Non-production Auto Scaling group or EKS node group running only On-Demand | live Spot price history | no |
| aws | `stale-synthetics-canary` | pro | Synthetics canaries failing every run | live list price | no |
| aws | `unused-shield-advanced` | pro | Shield Advanced subscriptions with no protected resources | live list price | no |
| aws | `vpn-tunnels-down` | pro | Site-to-Site VPN connections with every tunnel down | unpriced | no |
| aws | `waf-unused-web-acl` | pro | WAFv2 web ACLs with nothing attached | live list price | no |
| aws | `workspace-billing-mode` | pro | WorkSpaces on the wrong billing mode | live list price | no |
| aws | `commitments` | team | Savings Plans and Reserved Instances to buy | live list price | no |
| aws | `commitments-expiring` | team | Reserved Instances and Savings Plans about to end | live list price | no |
| aws | `off-hours` | team | Databases idle at the same hours every week | live list price | no |
| azure | `azure-acr-premium-unneeded` | pro (preview) | Premium Container Registry on Standard fit | live list price | no |
| azure | `azure-advisor` | pro (preview) | Azure Advisor Cost recommendations nothing else covers | provider estimate | no |
| azure | `azure-aks-autoscaler-min-count` | pro (preview) | AKS nodes the autoscaler marks unneeded held by pool minimums | live list price | no |
| azure | `azure-aks-standard-tier-nonprod` | pro (preview) | Non-production AKS clusters on the paid Standard tier | live list price | no |
| azure | `azure-app-service-premium-v2` | pro (preview) | App Service plans on Premium v2, priced against the same-size Premium v3 | live list price | no |
| azure | `azure-backup-orphaned-vm` | pro (preview) | Azure VM backups still billed after the VM was deleted | live list price | no |
| azure | `azure-cold-hot-blob-account` | pro (preview) | Hot blob data with no reads for 30 days | live list price | no |
| azure | `azure-commitment-underused` | pro (preview) | Reservations and savings plans paid for but not used | live list price | no |
| azure | `azure-cosmos-extra-regions-nonprod` | pro (preview) | Non-production Cosmos DB account replicated to more than one region | live list price | no |
| azure | `azure-cosmos-overprovisioned-throughput` | pro (preview) | Cosmos DB overprovisioned throughput | live list price | no |
| azure | `azure-deleted-managed-hsm` | pro (preview) | Deleted Managed HSM pools still billed until purge | live list price | no |
| azure | `azure-disk-provisioned-performance` | pro (preview) | Premium SSD v2 or Ultra disks over-provisioned | live list price | no |
| azure | `azure-empty-aks-standard` | pro (preview) | Empty AKS Standard clusters | live list price | no |
| azure | `azure-empty-app-service-plan` | pro (preview) | Empty paid Linux App Service plans | live list price | yes |
| azure | `azure-empty-ase` | pro (preview) | Empty App Service Environment v3 | live list price | no |
| azure | `azure-empty-dedicated-host` | pro (preview) | Empty dedicated hosts | live list price | no |
| azure | `azure-empty-functions-premium-plan` | pro (preview) | Empty Azure Functions Premium plans | live list price | yes |
| azure | `azure-empty-public-dns-zone` | pro (preview) | Empty public DNS zones | live list price | no |
| azure | `azure-empty-sql-elastic-pool` | pro (preview) | Empty Standard DTU SQL elastic pools | live list price | no |
| azure | `azure-empty-standard-lb` | pro (preview) | Empty Standard load balancers | live list price | no |
| azure | `azure-empty-virtual-hub` | pro (preview) | Empty Standard virtual WAN hubs | live list price | no |
| azure | `azure-empty-windows-app-service-plan` | pro (preview) | Empty paid Windows App Service plans | live list price | yes |
| azure | `azure-firewall-premium-unneeded` | pro (preview) | Premium Azure Firewalls on Standard fit | live list price | no |
| azure | `azure-flexible-server-ha-nonprod` | pro (preview) | High availability on non-production PostgreSQL/MySQL flexible servers | live list price | no |
| azure | `azure-geo-redundant-nonprod` | pro (preview) | Geo-redundant storage on a non-production account | live list price | no |
| azure | `azure-hybrid-benefit-unused` | pro (preview) | PAYG Windows or SQL licences without Azure Hybrid Benefit | live list price | yes |
| azure | `azure-idle-ai-search` | pro (preview) | Idle Azure AI Search | live list price | no |
| azure | `azure-idle-aks-cluster` | pro (preview) | Idle AKS clusters | live list price | no |
| azure | `azure-idle-analysis-services` | pro (preview) | Idle Analysis Services servers | live list price | no |
| azure | `azure-idle-api-management` | pro (preview) | Idle API Management | live list price | no |
| azure | `azure-idle-app-gateway` | pro (preview) | Idle Application Gateway v2 | live list price | no |
| azure | `azure-idle-app-service-plan` | pro (preview) | Idle paid Linux App Service plans | live list price | no |
| azure | `azure-idle-bastion` | pro (preview) | Idle Basic Bastion | live list price | no |
| azure | `azure-idle-container-app-min-replicas` | pro (preview) | Idle Consumption Container Apps with minimum replicas | live list price | yes |
| azure | `azure-idle-container-instances` | pro (preview) | Idle Container Instances | live list price | yes |
| azure | `azure-idle-data-explorer` | pro (preview) | Idle Azure Data Explorer clusters | unpriced | no |
| azure | `azure-idle-eventhubs-premium` | pro (preview) | Idle Event Hubs Premium namespaces | live list price | no |
| azure | `azure-idle-expressroute` | pro (preview) | Idle ExpressRoute circuits | live list price | no |
| azure | `azure-idle-expressroute-gateway` | pro (preview) | ExpressRoute gateways with no connections | live list price | no |
| azure | `azure-idle-firewall` | pro (preview) | Idle Standard/Premium Azure Firewalls | live list price | no |
| azure | `azure-idle-flexible-server` | pro (preview) | Idle PostgreSQL/MySQL flexible servers | live list price | yes |
| azure | `azure-idle-front-door` | pro (preview) | Idle Front Door Standard/Premium | live list price | no |
| azure | `azure-idle-managed-hsm` | pro (preview) | Managed HSM pools nobody uses | live list price | no |
| azure | `azure-idle-nat-gateway` | pro (preview) | Idle NAT gateways | live list price | no |
| azure | `azure-idle-openai-ptu` | pro (preview) | Idle Azure OpenAI provisioned throughput deployments | live list price | yes |
| azure | `azure-idle-redis` | pro (preview) | Idle Azure Cache for Redis | live list price | no |
| azure | `azure-idle-running-vm` | pro (preview) | Idle running VMs | live list price | no |
| azure | `azure-idle-servicebus-premium` | pro (preview) | Idle Service Bus Premium namespaces | live list price | no |
| azure | `azure-idle-sql-database` | pro (preview) | Idle DTU SQL databases | live list price | no |
| azure | `azure-idle-sql-elastic-pool` | pro (preview) | Idle Standard DTU SQL elastic pools | live list price | no |
| azure | `azure-idle-sql-managed-instance` | pro (preview) | Idle SQL Managed Instances | live list price | no |
| azure | `azure-idle-sql-vcore` | pro (preview) | Idle provisioned Gen5 vCore SQL databases | live list price | no |
| azure | `azure-idle-ssis-integration-runtime` | pro (preview) | Idle Azure-SSIS integration runtimes | live list price | no |
| azure | `azure-idle-stream-analytics` | pro (preview) | Idle Stream Analytics jobs | live list price | no |
| azure | `azure-idle-synapse-dedicated-pool` | pro (preview) | Idle Synapse dedicated SQL pools | live list price | yes |
| azure | `azure-idle-vmss` | pro (preview) | Idle Uniform virtual machine scale sets | live list price | no |
| azure | `azure-idle-windows-app-service-plan` | pro (preview) | Idle paid Windows App Service plans | live list price | no |
| azure | `azure-large-hot-storage-no-lifecycle` | pro (preview) | Large Hot storage without lifecycle | cost-only | no |
| azure | `azure-ml-idle-min-nodes` | pro (preview) | Azure ML compute cluster keeping idle minimum nodes | live list price | no |
| azure | `azure-netapp-pool-unallocated` | pro (preview) | NetApp capacity pools with unallocated space | live list price | no |
| azure | `azure-old-snapshot` | pro (preview) | Old managed-disk snapshots | unpriced | no |
| azure | `azure-oversized-app-service-plan` | pro (preview) | Oversized paid App Service plans | live list price | no |
| azure | `azure-oversized-eventhubs-premium` | pro (preview) | Oversized Event Hubs Premium namespaces | live list price | no |
| azure | `azure-oversized-flexible-server` | pro (preview) | Oversized PostgreSQL/MySQL flexible servers | live list price | no |
| azure | `azure-oversized-redis` | pro (preview) | Oversized Azure Cache for Redis | live list price | no |
| azure | `azure-oversized-running-vm` | pro (preview) | Oversized running VMs | live list price | no |
| azure | `azure-oversized-servicebus-premium` | pro (preview) | Oversized Service Bus Premium namespaces | live list price | no |
| azure | `azure-oversized-sql-dtu` | pro (preview) | Oversized DTU SQL databases | live list price | no |
| azure | `azure-oversized-sql-managed-instance` | pro (preview) | Oversized Gen5 SQL Managed Instances | live list price | no |
| azure | `azure-oversized-sql-vcore` | pro (preview) | Oversized provisioned Gen5 vCore SQL databases | live list price | no |
| azure | `azure-oversized-vmss` | pro (preview) | Azure scale set one size too big | live list price | no |
| azure | `azure-premium-disk-underused` | pro (preview) | Premium SSD disks that fit Standard SSD | live list price | no |
| azure | `azure-premium-fileshare-overprovisioned` | pro (preview) | Premium v1 file shares over-provisioned | live list price | no |
| azure | `azure-premium-snapshot` | pro (preview) | Premium managed-disk snapshots | unpriced | no |
| azure | `azure-previous-generation-vm` | pro (preview) | Running VMs on sizes Microsoft is retiring, priced above a current same-shape size | live list price | no |
| azure | `azure-retained-disk` | pro (preview) | Disks kept on deallocated VMs | unpriced | no |
| azure | `azure-spot-candidate-nonprod` | pro (preview) | Non-production scale set or AKS node pool without Spot | live list price | no |
| azure | `azure-stopped-allocated-vm` | pro (preview) | Stopped-but-allocated VMs | live list price | yes |
| azure | `azure-unattached-disk` | pro (preview) | Unattached managed disks | live list price | yes |
| azure | `azure-unprovisioned-expressroute` | pro (preview) | Unprovisioned ExpressRoute circuits still billing | live list price | no |
| azure | `azure-unused-capacity-reservation` | pro (preview) | Unused capacity reservations | live list price | no |
| azure | `azure-unused-ddos-plan` | pro (preview) | Unused DDoS Network Protection plans | live list price | no |
| azure | `azure-unused-private-dns-zone` | pro (preview) | Unused private DNS zones | live list price | no |
| azure | `azure-unused-private-endpoint` | pro (preview) | Unused private endpoints | live list price | no |
| azure | `azure-unused-public-ip` | pro (preview) | Unused Standard static public IPv4 | live list price | yes |
| azure | `azure-unused-public-ip-prefix` | pro (preview) | Unused Standard public IP prefixes | live list price | no |
| azure | `azure-vpn-no-connections` | pro (preview) | VPN gateways with no connections | live list price | no |
| gcp | `gcp-artifact-registry-no-cleanup` | pro (preview) | Large Artifact Registry repositories without cleanup | cost-only | no |
| gcp | `gcp-bigquery-idle-reservation` | pro (preview) | BigQuery reservation paying for baseline slots no project uses | catalog snapshot | no |
| gcp | `gcp-bigquery-storage-billing` | pro (preview) | BigQuery datasets on the more expensive storage billing model | catalog snapshot | no |
| gcp | `gcp-bigquery-time-travel-window` | pro (preview) | BigQuery time-travel windows longer than needed on physical billing | catalog snapshot | no |
| gcp | `gcp-bucket-incomplete-multipart` | pro (preview) | Cloud Storage incomplete multipart uploads | catalog snapshot | no |
| gcp | `gcp-bucket-noncurrent-no-lifecycle` | pro (preview) | Cloud Storage noncurrent versions without expiry | catalog snapshot | no |
| gcp | `gcp-bucket-soft-delete-bytes` | pro (preview) | Cloud Storage soft-deleted object bytes | catalog snapshot | no |
| gcp | `gcp-cloudsql-enterprise-plus-unneeded` | pro (preview) | Cloud SQL Enterprise Plus without data cache | catalog snapshot | no |
| gcp | `gcp-cloudsql-extended-support` | pro (preview) | Cloud SQL on a version in extended support | catalog snapshot | no |
| gcp | `gcp-cloudsql-ha-nonprod` | pro (preview) | Regional HA on non-production Cloud SQL | catalog snapshot | no |
| gcp | `gcp-cold-standard-bucket` | pro (preview) | Standard buckets with no object reads for 30 days | catalog snapshot | no |
| gcp | `gcp-empty-gke-regional` | pro (preview) | Empty regional GKE Standard clusters | catalog snapshot | no |
| gcp | `gcp-empty-sole-tenant-node` | pro (preview) | Empty sole-tenant nodes | catalog snapshot | no |
| gcp | `gcp-gke-extended-support` | pro (preview) | GKE clusters on the Extended channel past standard support | catalog snapshot | no |
| gcp | `gcp-gke-nodepool-underrequested` | pro (preview) | GKE node pool without autoscaling whose pods request far less than its nodes offer | catalog snapshot | no |
| gcp | `gcp-gke-overrequested` | pro (preview) | GKE Standard clusters whose nodes are held by requests pods do not use | catalog snapshot | no |
| gcp | `gcp-hyperdisk-overprovisioned` | pro (preview) | Over-provisioned Hyperdisk performance | catalog snapshot | no |
| gcp | `gcp-idle-alloydb` | pro (preview) | Idle AlloyDB primaries | catalog snapshot | no |
| gcp | `gcp-idle-bigtable` | pro (preview) | Idle Bigtable instances | catalog snapshot | no |
| gcp | `gcp-idle-classic-vpn-tunnel` | pro (preview) | Idle Classic VPN tunnels | catalog snapshot | no |
| gcp | `gcp-idle-cloudrun-min` | pro (preview) | Idle Cloud Run minimum instances | catalog snapshot | no |
| gcp | `gcp-idle-cloudsql-mysql` | pro (preview) | Idle Cloud SQL MySQL | catalog snapshot | yes |
| gcp | `gcp-idle-cloudsql-postgres` | pro (preview) | Idle Cloud SQL PostgreSQL | catalog snapshot | yes |
| gcp | `gcp-idle-cloudsql-replica` | pro (preview) | Idle Cloud SQL read replica | catalog snapshot | no |
| gcp | `gcp-idle-cloudsql-sqlserver` | pro (preview) | Idle Cloud SQL SQL Server | catalog snapshot | yes |
| gcp | `gcp-idle-composer` | pro (preview) | Idle Cloud Composer 2 environments | catalog snapshot | no |
| gcp | `gcp-idle-data-fusion` | pro (preview) | Idle Cloud Data Fusion instances | catalog snapshot | no |
| gcp | `gcp-idle-dataflow-streaming` | pro (preview) | Idle Dataflow streaming jobs | catalog snapshot | no |
| gcp | `gcp-idle-dataproc` | pro (preview) | Idle Dataproc clusters | catalog snapshot | yes |
| gcp | `gcp-idle-dataproc-metastore` | pro (preview) | Idle Dataproc Metastore services | catalog snapshot | no |
| gcp | `gcp-idle-filestore` | pro (preview) | Idle Filestore | catalog snapshot | no |
| gcp | `gcp-idle-ha-vpn-tunnel` | pro (preview) | Idle HA VPN tunnels | catalog snapshot | no |
| gcp | `gcp-idle-http-forwarding-rule` | pro (preview) | Idle HTTP(S) forwarding rules | catalog snapshot | no |
| gcp | `gcp-idle-interconnect-attachment` | pro (preview) | Idle Interconnect VLAN attachments | catalog snapshot | no |
| gcp | `gcp-idle-memcached` | pro (preview) | Idle Memorystore for Memcached | unpriced | no |
| gcp | `gcp-idle-memorystore-redis` | pro (preview) | Idle Memorystore Redis | catalog snapshot | no |
| gcp | `gcp-idle-memorystore-redis-standard` | pro (preview) | Idle Memorystore Redis Standard | catalog snapshot | no |
| gcp | `gcp-idle-mig` | pro (preview) | Idle managed instance groups | catalog snapshot | no |
| gcp | `gcp-idle-public-nat` | pro (preview) | Idle Public NAT gateways | catalog snapshot | no |
| gcp | `gcp-idle-redis-cluster` | pro (preview) | Idle Memorystore for Redis Cluster | catalog snapshot | no |
| gcp | `gcp-idle-regional-lb-forwarding-rule` | pro (preview) | Idle regional LB forwarding rules | unpriced | no |
| gcp | `gcp-idle-running-vm` | pro (preview) | Idle running VMs | catalog snapshot | yes |
| gcp | `gcp-idle-spanner` | pro (preview) | Idle Spanner instances | catalog snapshot | no |
| gcp | `gcp-idle-ssd-to-balanced` | pro (preview) | Idle pd-ssd that fit pd-balanced | catalog snapshot | no |
| gcp | `gcp-idle-tpu` | pro (preview) | Cloud TPUs with idle TensorCores | catalog snapshot | no |
| gcp | `gcp-idle-valkey` | pro (preview) | Idle Memorystore for Valkey | catalog snapshot | no |
| gcp | `gcp-idle-vector-search-endpoint` | pro (preview) | Idle Vector Search deployed indexes | catalog snapshot | no |
| gcp | `gcp-idle-vertex-endpoint` | pro (preview) | Idle Vertex AI endpoints | catalog snapshot | no |
| gcp | `gcp-idle-workbench-instance` | pro (preview) | Idle Vertex AI Workbench instances | catalog snapshot | yes |
| gcp | `gcp-large-standard-bucket-no-lifecycle` | pro (preview) | Large Standard buckets without lifecycle | cost-only | no |
| gcp | `gcp-log-ingest` | pro (preview) | Logs that cost the most to ingest into Cloud Logging | catalog snapshot | no |
| gcp | `gcp-log-retention` | pro (preview) | Log buckets kept longer than the free 30 days | catalog snapshot | no |
| gcp | `gcp-multiregion-bucket-nonprod` | pro (preview) | Non-production bucket in a multi-region | catalog snapshot | no |
| gcp | `gcp-old-custom-image` | pro (preview) | Old custom images | catalog snapshot | no |
| gcp | `gcp-old-machine-image` | pro (preview) | Old machine images | catalog snapshot | no |
| gcp | `gcp-old-snapshot` | pro (preview) | Old disk snapshots | catalog snapshot | no |
| gcp | `gcp-oversized-alloydb` | pro (preview) | Oversized AlloyDB instances | catalog snapshot | no |
| gcp | `gcp-oversized-bigtable` | pro (preview) | Oversized Bigtable clusters | catalog snapshot | no |
| gcp | `gcp-oversized-cloudsql` | pro (preview) | Oversized Cloud SQL | catalog snapshot | no |
| gcp | `gcp-oversized-filestore` | pro (preview) | Oversized Filestore instances | catalog snapshot | no |
| gcp | `gcp-oversized-memorystore-redis` | pro (preview) | Oversized Memorystore Redis | catalog snapshot | no |
| gcp | `gcp-oversized-mig` | pro (preview) | Managed instance group one size too big | catalog snapshot | no |
| gcp | `gcp-oversized-running-vm` | pro (preview) | Oversized running VMs | catalog snapshot | no |
| gcp | `gcp-oversized-spanner` | pro (preview) | Oversized Spanner instances | catalog snapshot | no |
| gcp | `gcp-public-ipv4-in-use` | pro (preview) | Public IPv4 on VMs that may not need it | catalog snapshot | no |
| gcp | `gcp-recommender` | pro (preview) | GCP Recommender cost recommendations nothing else covers (idle SQL/image/reservation/GKE, Run, CUD purchase advice, bucket soft-delete) | provider estimate | no |
| gcp | `gcp-retained-disk` | pro (preview) | Disks kept on stopped VMs | unpriced | no |
| gcp | `gcp-spot-candidate-nonprod` | pro (preview) | Non-production MIGs and GKE node pools on standard VMs that may fit Spot | catalog snapshot | no |
| gcp | `gcp-stopped-cloudsql-storage` | pro (preview) | Stopped Cloud SQL storage | catalog snapshot | no |
| gcp | `gcp-unattached-disk` | pro (preview) | Unattached Persistent Disks | catalog snapshot | yes |
| gcp | `gcp-unused-public-ip` | pro (preview) | Unused static public IPv4 | catalog snapshot | yes |
| gcp | `gcp-unused-reservation` | pro (preview) | Unused Compute Engine reservations | catalog snapshot | no |
