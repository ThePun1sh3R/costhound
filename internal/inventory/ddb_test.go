package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDDB struct{}

func (fakeDDB) ListTables(context.Context, *dynamodb.ListTablesInput, ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	return &dynamodb.ListTablesOutput{TableNames: []string{"orders"}}, nil
}

func (fakeDDB) DescribeTable(_ context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return &dynamodb.DescribeTableOutput{Table: &ddbtypes.TableDescription{TableName: in.TableName, TableStatus: ddbtypes.TableStatusActive,
		TableSizeBytes:     aws.Int64(100),
		BillingModeSummary: &ddbtypes.BillingModeSummary{BillingMode: ddbtypes.BillingModePayPerRequest},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndexDescription{{IndexName: aws.String("by-user"), IndexSizeBytes: aws.Int64(50), IndexStatus: ddbtypes.IndexStatusActive,
			ProvisionedThroughput: &ddbtypes.ProvisionedThroughputDescription{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(3)}}},
		LocalSecondaryIndexes: []ddbtypes.LocalSecondaryIndexDescription{{IndexName: aws.String("by-date"), IndexSizeBytes: aws.Int64(7)}},
		Replicas:              []ddbtypes.ReplicaDescription{{RegionName: aws.String("us-east-1")}},
		TableClassSummary: &ddbtypes.TableClassSummary{TableClass: ddbtypes.TableClassStandardInfrequentAccess,
			LastUpdateDateTime: aws.Time(time.Now().AddDate(0, 0, -3))},
	}}, nil
}

// What dynamodb-table-class needs comes from DescribeTable alone.
func TestLoadTablesSizesAndClass(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	if err := loadTables(context.Background(), r, fakeDDB{}, nil); err != nil {
		t.Fatal(err)
	}
	tb := r.Tables[0]
	if tb.Bytes != 150 || tb.LSIBytes != 7 || tb.Status != "ACTIVE" || !tb.Global || tb.Provisioned ||
		tb.Class != "STANDARD_INFREQUENT_ACCESS" || tb.ClassChanged == nil {
		t.Errorf("table: %+v", tb)
	}
	if len(tb.GSIs) != 1 || tb.GSIs[0] != (GSI{Name: "by-user", Status: "ACTIVE", Bytes: 50, RCU: 5, WCU: 3}) || len(tb.Replicas) != 1 || tb.Replicas[0] != "us-east-1" {
		t.Errorf("each index on its own, and the replicas: %+v %v", tb.GSIs, tb.Replicas)
	}
}
