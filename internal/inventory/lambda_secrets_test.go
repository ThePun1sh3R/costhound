package inventory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type secretLambda struct{}

func (secretLambda) ListFunctions(context.Context, *lambda.ListFunctionsInput, ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	return &lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{{
		FunctionName:  aws.String("orders-api"),
		Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
		Environment:   &lambdatypes.EnvironmentResponse{Variables: map[string]string{"DB_PASSWORD": "hunter2-env"}},
		ImageConfigResponse: &lambdatypes.ImageConfigResponse{ImageConfig: &lambdatypes.ImageConfig{
			Command: []string{"serve", "--token=hunter2-cmd"}, EntryPoint: []string{"/bin/app"}}},
	}}}, nil
}

// ListFunctions answers with environment variables and an image's command:
// they're dropped as the page arrives, and what the checks read stays.
func TestLambdaSecretsAreDroppedOnArrival(t *testing.T) {
	r := Load(context.Background(), "eu-west-1", "111111111111", Clients{EC2: &fakeEC2{}, Logs: deniedLogs{}, ELB: fakeELB{}, Lambda: secretLambda{}})
	b, _ := json.Marshal(r.Functions)
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("a secret-capable setting was kept: %s", b)
	}
	if len(r.Functions) != 1 || aws.ToString(r.Functions[0].FunctionName) != "orders-api" || len(r.Functions[0].Architectures) != 1 {
		t.Errorf("what lambda-x86 reads must stay: %+v", r.Functions)
	}
}
