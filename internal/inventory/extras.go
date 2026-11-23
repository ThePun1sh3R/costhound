package inventory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aatypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

// SecretsAPI, ECRAPI and LambdaAPI are the calls the extra free checks use.
type SecretsAPI interface {
	secretsmanager.ListSecretsAPIClient
}

type ECRAPI interface {
	ecr.DescribeRepositoriesAPIClient
	ecr.DescribeImagesAPIClient
	GetLifecyclePolicy(context.Context, *ecr.GetLifecyclePolicyInput, ...func(*ecr.Options)) (*ecr.GetLifecyclePolicyOutput, error)
}

type LambdaAPI interface {
	lambda.ListFunctionsAPIClient
}

type DynamoDBAPI interface {
	dynamodb.ListTablesAPIClient
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
}

type AutoScalingAPI interface {
	applicationautoscaling.DescribeScalableTargetsAPIClient
}

// Table is a DynamoDB table with its provisioned capacity (indexes included).
type Table struct {
	Name, Class string
	Provisioned bool
	RCU, WCU    int64    // table + every global secondary index
	Indexes     []string // global secondary index names
	AutoScaled  bool
	Created     *time.Time
	// For dynamodb-table-class (DescribeTable updates sizes about every six hours):
	Bytes        int64      // the table and its global secondary indexes
	LSIBytes     int64      // its local secondary indexes, which Bytes may already hold
	Status       string     // ACTIVE, UPDATING, ...
	Global       bool       // a global table (Replicas or a GlobalTableVersion)
	ClassChanged *time.Time // when its table class last changed
	GSIs         []GSI      // each global secondary index (idle-dynamodb-index)
	Replicas     []string   // a global table's replica regions
}

// GSI is a global secondary index: its own size, state and provisioned capacity.
type GSI struct {
	Name, Status string
	Bytes        int64
	RCU, WCU     int64
}

type BackupAPI interface {
	backup.ListBackupVaultsAPIClient
	backup.ListRecoveryPointsByBackupVaultAPIClient
}

type EFSAPI interface {
	efs.DescribeFileSystemsAPIClient
}

type KinesisAPI interface {
	kinesis.ListStreamsAPIClient
	DescribeStreamSummary(context.Context, *kinesis.DescribeStreamSummaryInput, ...func(*kinesis.Options)) (*kinesis.DescribeStreamSummaryOutput, error)
	DescribeAccountSettings(context.Context, *kinesis.DescribeAccountSettingsInput, ...func(*kinesis.Options)) (*kinesis.DescribeAccountSettingsOutput, error)
}

// FastRestore is a snapshot with Fast Snapshot Restore on in one AZ.
type FastRestore struct {
	SnapshotID, AZ, State string
	Enabled               *time.Time
}

// RecoveryPoint is one AWS Backup recovery point.
type RecoveryPoint struct {
	Vault, ARN, ResourceArn, ResourceType string
	Bytes                                 int64
	Created                               *time.Time
}

// FileSystem is an EFS file system.
type FileSystem struct {
	ID, Name, ThroughputMode string
	MountTargets             int
	Standard, IA, Archive    int64 // bytes per storage class
	ProvisionedMiBps         float64
	Created                  *time.Time
}

// Stream is a Kinesis data stream.
type Stream struct {
	Name, Mode string // Mode: PROVISIONED or ON_DEMAND
	Shards     int
	Created    *time.Time
}

// KinesisOnDemandBilling is the account's on-demand pricing mode in one region.
type KinesisOnDemandBilling int

const (
	KinesisOnDemandStandard KinesisOnDemandBilling = iota
	KinesisOnDemandAdvantage
	KinesisOnDemandBillingUnknown
)

type ElastiCacheAPI interface {
	elasticache.DescribeCacheClustersAPIClient
}

// CacheCluster is one ElastiCache cluster (a node group member when part of a
// replication group).
type CacheCluster struct {
	ID, Engine, Version, NodeType, ReplicationGroup string
	Nodes                                           int
	Created                                         *time.Time
	NodeIDs                                         []string // "0001", ...; empty when the API didn't list them
}

// S3API is the bucket calls; buckets are listed once per account and read
// through a client in each bucket's own region.
type S3API interface {
	s3.ListBucketsAPIClient
	GetBucketLifecycleConfiguration(context.Context, *s3.GetBucketLifecycleConfigurationInput, ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error)
	GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	GetObjectLockConfiguration(context.Context, *s3.GetObjectLockConfigurationInput, ...func(*s3.Options)) (*s3.GetObjectLockConfigurationOutput, error)
}

// Repo is an ECR repository with what its untagged images weigh.
type Repo struct {
	Name, URI     string
	HasLifecycle  bool
	UntaggedBytes int64
	UntaggedCount int
}

// Bucket is an S3 bucket and its lifecycle rules.
type Bucket struct {
	Name, Region string
	Created      *time.Time
	HasLifecycle bool // any enabled rule
	AbortsMPU    bool // an enabled rule aborts incomplete multipart uploads

	Versioning        string // "Enabled", "Suspended", "" (never versioned), "?" (unread)
	MFADelete         bool   // MFA Delete is on: S3 runs no lifecycle rules on the bucket
	ExpiresNoncurrent bool   // an enabled rule expires noncurrent versions (or the rules couldn't be read)
	ObjectLock        string // "Enabled", "" (none), "?" (unread); read only for versioned buckets that keep every version
}

func loadExtras(ctx context.Context, r *Region, c Clients, skip func(string, error)) {
	if c.Secrets != nil {
		p := secretsmanager.NewListSecretsPaginator(c.Secrets, &secretsmanager.ListSecretsInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *secretsmanager.ListSecretsOutput) {
			for _, s := range o.SecretList {
				if s.DeletedDate == nil {
					r.Secrets = append(r.Secrets, s)
				}
			}
		}); err != nil {
			skip("secretsmanager:ListSecrets", err)
		}
	}
	if c.ECR != nil {
		if err := loadRepos(ctx, r, c.ECR); err != nil {
			skip("ecr:DescribeRepositories", err)
		}
	}
	if c.DynamoDB != nil {
		if err := loadTables(ctx, r, c.DynamoDB, c.AutoScaling); err != nil {
			skip("dynamodb:ListTables", err)
		}
	}
	if c.FastRestore != nil {
		// Filtered here, not with a state filter, which not every EC2-compatible endpoint supports.
		p := ec2.NewDescribeFastSnapshotRestoresPaginator(c.FastRestore, &ec2.DescribeFastSnapshotRestoresInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ec2.DescribeFastSnapshotRestoresOutput) {
			for _, f := range o.FastSnapshotRestores {
				if st := string(f.State); st != "enabling" && st != "optimizing" && st != "enabled" {
					continue // disabling/disabled are no longer billed
				}
				r.FastRestores = append(r.FastRestores, FastRestore{SnapshotID: aws.ToString(f.SnapshotId), AZ: aws.ToString(f.AvailabilityZone), State: string(f.State), Enabled: f.EnabledTime})
			}
		}); err != nil {
			skip("ec2:DescribeFastSnapshotRestores", err)
		}
	}
	if c.Backup != nil {
		if err := loadRecoveryPoints(ctx, r, c.Backup); err != nil {
			skip("backup:ListBackupVaults", err)
		}
	}
	if c.EFS != nil {
		p := efs.NewDescribeFileSystemsPaginator(c.EFS, &efs.DescribeFileSystemsInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *efs.DescribeFileSystemsOutput) {
			for _, f := range o.FileSystems {
				if f.LifeCycleState != "available" {
					continue
				}
				fs := FileSystem{ID: aws.ToString(f.FileSystemId), Name: aws.ToString(f.Name), MountTargets: int(f.NumberOfMountTargets),
					ThroughputMode: string(f.ThroughputMode), ProvisionedMiBps: aws.ToFloat64(f.ProvisionedThroughputInMibps), Created: f.CreationTime}
				if z := f.SizeInBytes; z != nil {
					fs.Standard, fs.IA, fs.Archive = aws.ToInt64(z.ValueInStandard), aws.ToInt64(z.ValueInIA), aws.ToInt64(z.ValueInArchive)
					if fs.Standard+fs.IA+fs.Archive == 0 {
						fs.Standard = z.Value
					}
				}
				r.FileSystems = append(r.FileSystems, fs)
			}
		}); err != nil {
			skip("elasticfilesystem:DescribeFileSystems", err)
		}
	}
	if c.Kinesis != nil {
		if err := loadStreams(ctx, r, c.Kinesis); err != nil {
			skip("kinesis:ListStreams", err)
		}
	}
	if c.ElastiCache != nil {
		p := elasticache.NewDescribeCacheClustersPaginator(c.ElastiCache, &elasticache.DescribeCacheClustersInput{ShowCacheNodeInfo: aws.Bool(true)})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *elasticache.DescribeCacheClustersOutput) {
			for _, cc := range o.CacheClusters {
				if st := aws.ToString(cc.CacheClusterStatus); st == "deleting" || st == "deleted" {
					continue
				}
				r.CacheClusters = append(r.CacheClusters, CacheCluster{
					ID: aws.ToString(cc.CacheClusterId), Engine: aws.ToString(cc.Engine), Version: aws.ToString(cc.EngineVersion),
					NodeType: aws.ToString(cc.CacheNodeType), ReplicationGroup: aws.ToString(cc.ReplicationGroupId),
					Nodes: int(aws.ToInt32(cc.NumCacheNodes)), Created: cc.CacheClusterCreateTime, NodeIDs: cacheNodeIDs(cc.CacheNodes),
				})
			}
		}); err != nil {
			skip("elasticache:DescribeCacheClusters", err)
		} else {
			r.exists("CacheClusterId", liveIDs(r.CacheClusters, func(cc CacheCluster) (string, bool) { return cc.ID, true }))
		}
	}
	if c.Lambda != nil {
		p := lambda.NewListFunctionsPaginator(c.Lambda, &lambda.ListFunctionsInput{})
		if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *lambda.ListFunctionsOutput) {
			for _, fn := range o.Functions {
				// ListFunctions also returns settings that can hold secrets:
				// environment variables, and a container image's command and
				// entry point. No check reads them, so they're dropped as they
				// arrive, never kept for the rest of the scan.
				fn.Environment, fn.ImageConfigResponse = nil, nil
				r.Functions = append(r.Functions, fn)
			}
		}); err != nil {
			skip("lambda:ListFunctions", err)
		} else {
			var names []string
			for _, fn := range r.Functions {
				names = append(names, aws.ToString(fn.FunctionName))
			}
			r.exists("FunctionName", names)
		}
	}
}

func loadRepos(ctx context.Context, r *Region, c ECRAPI) error {
	var repos []ecrtypes.Repository
	p := ecr.NewDescribeRepositoriesPaginator(c, &ecr.DescribeRepositoriesInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *ecr.DescribeRepositoriesOutput) {
		repos = append(repos, o.Repositories...)
	}); err != nil {
		return err
	}
	for _, rp := range repos {
		repo := Repo{Name: aws.ToString(rp.RepositoryName), URI: aws.ToString(rp.RepositoryUri)}
		_, err := c.GetLifecyclePolicy(ctx, &ecr.GetLifecyclePolicyInput{RepositoryName: rp.RepositoryName})
		var notFound *ecrtypes.LifecyclePolicyNotFoundException
		switch {
		case err == nil:
			repo.HasLifecycle = true
		case errors.As(err, &notFound):
		default:
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "ecr:GetLifecyclePolicy", Error: ShortError(err)})
			repo.HasLifecycle = true // unknown: don't flag it
		}
		ip := ecr.NewDescribeImagesPaginator(c, &ecr.DescribeImagesInput{
			RepositoryName: rp.RepositoryName,
			Filter:         &ecrtypes.DescribeImagesFilter{TagStatus: ecrtypes.TagStatusUntagged},
		})
		if err := drain(ctx, ip.HasMorePages, ip.NextPage, func(o *ecr.DescribeImagesOutput) {
			for _, img := range o.ImageDetails {
				repo.UntaggedBytes += aws.ToInt64(img.ImageSizeInBytes)
				repo.UntaggedCount++
			}
		}); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "ecr:DescribeImages", Error: ShortError(err)})
		}
		r.Repos = append(r.Repos, repo)
	}
	return nil
}

// LoadBuckets lists the account's buckets once and reads each one's lifecycle
// rules with a client for its region (forRegion). Buckets are returned with
// their region so each lands in that region's report.
func LoadBuckets(ctx context.Context, list S3API, forRegion func(string) S3API) ([]Bucket, []Skip) {
	var out []Bucket
	var skips []Skip
	p := s3.NewListBucketsPaginator(list, &s3.ListBucketsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *s3.ListBucketsOutput) {
		for _, b := range o.Buckets {
			out = append(out, Bucket{Name: aws.ToString(b.Name), Region: aws.ToString(b.BucketRegion), Created: b.CreationDate})
		}
	}); err != nil {
		return nil, []Skip{{Region: "global", What: "s3:ListAllMyBuckets", Error: ShortError(err)}}
	}
	for i := range out {
		b := &out[i]
		if b.Region == "" {
			// Not every S3 endpoint returns BucketRegion in ListBuckets.
			b.Region = "us-east-1"
			if loc, err := list.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(b.Name)}); err == nil {
				switch c := string(loc.LocationConstraint); c {
				case "":
				case "EU":
					b.Region = "eu-west-1"
				default:
					b.Region = c
				}
			}
		}
		lc, err := forRegion(b.Region).GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(b.Name)})
		var api smithy.APIError
		switch {
		case err == nil:
			for _, rule := range lc.Rules {
				if rule.Status != "Enabled" {
					continue
				}
				b.HasLifecycle = true
				if rule.AbortIncompleteMultipartUpload != nil {
					b.AbortsMPU = true
				}
				if rule.NoncurrentVersionExpiration != nil {
					b.ExpiresNoncurrent = true
				}
			}
		case errors.As(err, &api) && api.ErrorCode() == "NoSuchLifecycleConfiguration":
		default:
			skips = append(skips, Skip{Region: b.Region, What: "s3:GetLifecycleConfiguration", Error: ShortError(err)})
			b.HasLifecycle, b.AbortsMPU, b.ExpiresNoncurrent = true, true, true // unknown: don't flag it
		}
		loadVersioning(ctx, b, forRegion(b.Region), &skips)
	}
	return out, skips
}

// loadVersioning reads a bucket's versioning and MFA Delete settings, and,
// for a versioned bucket that keeps every old version, its Object Lock
// setting (retention may be why the versions stay).
func loadVersioning(ctx context.Context, b *Bucket, c S3API, skips *[]Skip) {
	v, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(b.Name)})
	if err != nil {
		*skips = append(*skips, Skip{Region: b.Region, What: "s3:GetBucketVersioning", Error: ShortError(err)})
		b.Versioning = "?"
		return
	}
	b.Versioning, b.MFADelete = string(v.Status), v.MFADelete == s3types.MFADeleteStatusEnabled
	if (b.Versioning != "Enabled" && b.Versioning != "Suspended") || b.ExpiresNoncurrent {
		return
	}
	ol, err := c.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String(b.Name)})
	var api smithy.APIError
	switch {
	case err == nil:
		if ol.ObjectLockConfiguration != nil && ol.ObjectLockConfiguration.ObjectLockEnabled == s3types.ObjectLockEnabledEnabled {
			b.ObjectLock = "Enabled"
		}
	case errors.As(err, &api) && api.ErrorCode() == "ObjectLockConfigurationNotFoundError":
	default:
		*skips = append(*skips, Skip{Region: b.Region, What: "s3:GetBucketObjectLockConfiguration", Error: ShortError(err)})
		b.ObjectLock = "?"
	}
}

func loadTables(ctx context.Context, r *Region, c DynamoDBAPI, as AutoScalingAPI) error {
	var names []string
	p := dynamodb.NewListTablesPaginator(c, &dynamodb.ListTablesInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *dynamodb.ListTablesOutput) {
		names = append(names, o.TableNames...)
	}); err != nil {
		return err
	}
	r.exists("TableName", names) // before DescribeTable, which may fail for some
	if len(names) == 0 {
		return nil
	}
	scaled := map[string]bool{}
	if as != nil {
		sp := applicationautoscaling.NewDescribeScalableTargetsPaginator(as, &applicationautoscaling.DescribeScalableTargetsInput{ServiceNamespace: aatypes.ServiceNamespaceDynamodb})
		if err := drain(ctx, sp.HasMorePages, sp.NextPage, func(o *applicationautoscaling.DescribeScalableTargetsOutput) {
			for _, t := range o.ScalableTargets {
				// "table/orders" or "table/orders/index/by-user"
				parts := strings.Split(aws.ToString(t.ResourceId), "/")
				if len(parts) >= 2 {
					scaled[parts[1]] = true
				}
			}
		}); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "application-autoscaling:DescribeScalableTargets", Error: ShortError(err)})
			for _, n := range names {
				scaled[n] = true // unknown: don't flag
			}
		}
	}
	for _, n := range names {
		d, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(n)})
		if err != nil || d.Table == nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "dynamodb:DescribeTable", Error: ShortError(err)})
			continue
		}
		t := d.Table
		tb := Table{Name: n, AutoScaled: scaled[n], Created: t.CreationDateTime, Provisioned: true, Status: string(t.TableStatus),
			Bytes: aws.ToInt64(t.TableSizeBytes), Global: len(t.Replicas) > 0 || aws.ToString(t.GlobalTableVersion) != ""}
		if t.BillingModeSummary != nil && t.BillingModeSummary.BillingMode == "PAY_PER_REQUEST" {
			tb.Provisioned = false
		}
		if t.TableClassSummary != nil {
			tb.Class, tb.ClassChanged = string(t.TableClassSummary.TableClass), t.TableClassSummary.LastUpdateDateTime
		}
		if pt := t.ProvisionedThroughput; pt != nil {
			tb.RCU, tb.WCU = aws.ToInt64(pt.ReadCapacityUnits), aws.ToInt64(pt.WriteCapacityUnits)
		}
		for _, l := range t.LocalSecondaryIndexes {
			tb.LSIBytes += aws.ToInt64(l.IndexSizeBytes)
		}
		for _, g := range t.GlobalSecondaryIndexes {
			tb.Indexes = append(tb.Indexes, aws.ToString(g.IndexName))
			tb.Bytes += aws.ToInt64(g.IndexSizeBytes)
			gsi := GSI{Name: aws.ToString(g.IndexName), Status: string(g.IndexStatus), Bytes: aws.ToInt64(g.IndexSizeBytes)}
			if pt := g.ProvisionedThroughput; pt != nil {
				tb.RCU += aws.ToInt64(pt.ReadCapacityUnits)
				tb.WCU += aws.ToInt64(pt.WriteCapacityUnits)
				gsi.RCU, gsi.WCU = aws.ToInt64(pt.ReadCapacityUnits), aws.ToInt64(pt.WriteCapacityUnits)
			}
			tb.GSIs = append(tb.GSIs, gsi)
		}
		for _, rep := range t.Replicas {
			tb.Replicas = append(tb.Replicas, aws.ToString(rep.RegionName))
		}
		r.Tables = append(r.Tables, tb)
	}
	return nil
}

func loadRecoveryPoints(ctx context.Context, r *Region, c BackupAPI) error {
	var vaults []string
	p := backup.NewListBackupVaultsPaginator(c, &backup.ListBackupVaultsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *backup.ListBackupVaultsOutput) {
		for _, v := range o.BackupVaultList {
			if v.NumberOfRecoveryPoints > 0 {
				vaults = append(vaults, aws.ToString(v.BackupVaultName))
			}
		}
	}); err != nil {
		return err
	}
	for _, v := range vaults {
		rp := backup.NewListRecoveryPointsByBackupVaultPaginator(c, &backup.ListRecoveryPointsByBackupVaultInput{BackupVaultName: aws.String(v)})
		if err := drain(ctx, rp.HasMorePages, rp.NextPage, func(o *backup.ListRecoveryPointsByBackupVaultOutput) {
			for _, x := range o.RecoveryPoints {
				if x.Status != "COMPLETED" {
					continue
				}
				r.RecoveryPoints = append(r.RecoveryPoints, RecoveryPoint{Vault: v, ARN: aws.ToString(x.RecoveryPointArn), ResourceArn: aws.ToString(x.ResourceArn),
					ResourceType: aws.ToString(x.ResourceType), Bytes: aws.ToInt64(x.BackupSizeInBytes), Created: x.CreationDate})
			}
		}); err != nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "backup:ListRecoveryPointsByBackupVault", Error: ShortError(err)})
		}
	}
	return nil
}

func loadStreams(ctx context.Context, r *Region, c KinesisAPI) error {
	var names []string
	p := kinesis.NewListStreamsPaginator(c, &kinesis.ListStreamsInput{})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *kinesis.ListStreamsOutput) {
		names = append(names, o.StreamNames...)
	}); err != nil {
		return err
	}
	r.exists("StreamName", names) // before DescribeStreamSummary, which may fail for some
	hasOnDemand := false
	for _, n := range names {
		d, err := c.DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{StreamName: aws.String(n)})
		if err != nil || d.StreamDescriptionSummary == nil {
			r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "kinesis:DescribeStreamSummary", Error: ShortError(err)})
			continue
		}
		s := d.StreamDescriptionSummary
		st := Stream{Name: n, Mode: "PROVISIONED", Shards: int(aws.ToInt32(s.OpenShardCount)), Created: s.StreamCreationTimestamp}
		if s.StreamModeDetails != nil {
			st.Mode = string(s.StreamModeDetails.StreamMode)
		}
		hasOnDemand = hasOnDemand || st.Mode == string(kinesistypes.StreamModeOnDemand)
		r.Streams = append(r.Streams, st)
	}
	if !hasOnDemand {
		return nil
	}
	settings, err := c.DescribeAccountSettings(ctx, &kinesis.DescribeAccountSettingsInput{})
	if err != nil {
		r.KinesisOnDemandBilling = KinesisOnDemandBillingUnknown
		r.Skipped = append(r.Skipped, Skip{Region: r.Name, What: "kinesis:DescribeAccountSettings", Error: ShortError(err)})
		return nil
	}
	if commitment := settings.MinimumThroughputBillingCommitment; commitment != nil {
		switch commitment.Status {
		case kinesistypes.MinimumThroughputBillingCommitmentOutputStatusEnabled,
			kinesistypes.MinimumThroughputBillingCommitmentOutputStatusEnabledUntilEarliestAllowedEnd:
			r.KinesisOnDemandBilling = KinesisOnDemandAdvantage
		case kinesistypes.MinimumThroughputBillingCommitmentOutputStatusDisabled:
			r.KinesisOnDemandBilling = KinesisOnDemandStandard
		default:
			r.KinesisOnDemandBilling = KinesisOnDemandBillingUnknown
		}
	}
	return nil
}

func cacheNodeIDs(nodes []ectypes.CacheNode) []string {
	var out []string
	for _, n := range nodes {
		if id := aws.ToString(n.CacheNodeId); id != "" {
			out = append(out, id)
		}
	}
	return out
}
