module github.com/ThePun1sh3R/costhound

go 1.27.3

require (
	github.com/aws/aws-sdk-go-v2 v1.48.0
	github.com/aws/aws-sdk-go-v2/config v1.33.19
	github.com/aws/aws-sdk-go-v2/credentials v1.21.1
	github.com/aws/aws-sdk-go-v2/service/acm v1.52.0
	github.com/aws/aws-sdk-go-v2/service/acmpca v1.56.12 // pinned: newer versions speak Smithy RPC v2 CBOR, which moto can't serve; AWS accepts JSON 1.1
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.52.9
	github.com/aws/aws-sdk-go-v2/service/applicationautoscaling v1.51.12
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.82.0
	github.com/aws/aws-sdk-go-v2/service/backup v1.70.0
	github.com/aws/aws-sdk-go-v2/service/batch v1.79.1
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.70.1
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.65.12
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.76.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.92.2
	github.com/aws/aws-sdk-go-v2/service/costoptimizationhub v1.35.9
	github.com/aws/aws-sdk-go-v2/service/databasemigrationservice v1.75.9
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.74.1
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.348.0
	github.com/aws/aws-sdk-go-v2/service/ecr v1.69.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.106.0
	github.com/aws/aws-sdk-go-v2/service/efs v1.51.6
	github.com/aws/aws-sdk-go-v2/service/eks v1.105.6
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.66.0
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.42.0
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.64.0
	github.com/aws/aws-sdk-go-v2/service/emr v1.71.3
	github.com/aws/aws-sdk-go-v2/service/globalaccelerator v1.45.11
	github.com/aws/aws-sdk-go-v2/service/kafka v1.65.12
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.58.0
	github.com/aws/aws-sdk-go-v2/service/kms v1.63.0
	github.com/aws/aws-sdk-go-v2/service/lambda v1.117.0
	github.com/aws/aws-sdk-go-v2/service/memorydb v1.43.0
	github.com/aws/aws-sdk-go-v2/service/networkfirewall v1.81.0
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.88.1
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.41.7
	github.com/aws/aws-sdk-go-v2/service/organizations v1.63.0
	github.com/aws/aws-sdk-go-v2/service/pricing v1.51.6
	github.com/aws/aws-sdk-go-v2/service/rds v1.133.0
	github.com/aws/aws-sdk-go-v2/service/redshift v1.72.0
	github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi v1.42.0
	github.com/aws/aws-sdk-go-v2/service/rolesanywhere v1.35.0
	github.com/aws/aws-sdk-go-v2/service/route53 v1.71.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.116.0
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.291.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.51.0
	github.com/aws/aws-sdk-go-v2/service/sts v1.52.0
	github.com/aws/aws-sdk-go-v2/service/workspaces v1.83.3 // pinned: newer versions speak Smithy RPC v2 CBOR, which moto can't serve; AWS accepts JSON 1.1
	github.com/aws/smithy-go v1.28.9
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.27 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.9 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.25 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.39.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.44.5 // indirect
	golang.org/x/mod v0.43.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/telemetry v0.0.0-20261116191925-e77a43154acb // indirect
	golang.org/x/tools v0.53.0 // indirect
)

tool golang.org/x/tools/cmd/deadcode
