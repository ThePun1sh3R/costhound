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

type fakeLambda struct{}

func (fakeLambda) ListFunctions(context.Context, *lambda.ListFunctionsInput, ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	return &lambda.ListFunctionsOutput{Functions: []lambdatypes.FunctionConfiguration{{
		FunctionName: aws.String("api"), MemorySize: aws.Int32(512),
		Environment:         &lambdatypes.EnvironmentResponse{Variables: map[string]string{"DB_PASSWORD": "hunter2-env"}},
		ImageConfigResponse: &lambdatypes.ImageConfigResponse{ImageConfig: &lambdatypes.ImageConfig{Command: []string{"--token", "hunter2-cmd"}}},
	}}}, nil
}

// A function's environment and image arguments are never kept: no check
// needs them, and they can hold secrets.
func TestFunctionsKeepNoSecrets(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	loadExtras(context.Background(), r, Clients{Lambda: fakeLambda{}}, func(string, error) {})
	if len(r.Functions) != 1 || aws.ToInt32(r.Functions[0].MemorySize) != 512 {
		t.Fatalf("functions: %+v", r.Functions)
	}
	if b, _ := json.Marshal(r.Functions); strings.Contains(string(b), "hunter2") {
		t.Errorf("a secret was kept: %s", b)
	}
}
