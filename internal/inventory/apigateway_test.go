package inventory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	agtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	"github.com/aws/smithy-go"
)

type fakeAPIGW struct{ stageCalls int }

func (f *fakeAPIGW) GetRestApis(_ context.Context, in *apigateway.GetRestApisInput, _ ...func(*apigateway.Options)) (*apigateway.GetRestApisOutput, error) {
	if in.Position == nil { // two pages
		return &apigateway.GetRestApisOutput{Items: []agtypes.RestApi{{Id: aws.String("a1b2c3d4e5"), Name: aws.String("orders")},
			{Id: aws.String("f6g7h8i9j0"), Name: aws.String("orders")}}, Position: aws.String("p2")}, nil
	}
	return &apigateway.GetRestApisOutput{Items: []agtypes.RestApi{{Id: aws.String("k1l2m3n4o5"), Name: aws.String("Café API")},
		{Id: aws.String("p6q7r8s9t0"), Name: aws.String("注文")}}}, nil
}

func (f *fakeAPIGW) GetStages(_ context.Context, in *apigateway.GetStagesInput, _ ...func(*apigateway.Options)) (*apigateway.GetStagesOutput, error) {
	f.stageCalls++
	switch aws.ToString(in.RestApiId) {
	case "a1b2c3d4e5":
		return &apigateway.GetStagesOutput{Item: []agtypes.Stage{{StageName: aws.String("prod"), CacheClusterEnabled: true,
			CacheClusterSize: agtypes.CacheClusterSizeSize6Point1Gb, CacheClusterStatus: agtypes.CacheClusterStatusAvailable,
			MethodSettings: map[string]agtypes.MethodSetting{"*/*": {CachingEnabled: false}, "~1pets/GET": {CachingEnabled: true}},
			Variables:      map[string]string{"db_password": "hunter2-not-for-costhound"}, Tags: map[string]string{"team": "payments"}}}}, nil
	case "f6g7h8i9j0":
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException"}
	}
	return &apigateway.GetStagesOutput{}, nil
}

func TestLoadRestStages(t *testing.T) {
	r := &Region{Name: "eu-west-1"}
	var skips []string
	f := &fakeAPIGW{}
	loadRestStages(context.Background(), r, f, func(what string, _ error) { skips = append(skips, what) })
	if len(r.RestStages) != 1 || f.stageCalls != 4 {
		t.Fatalf("stages %+v after %d GetStages calls", r.RestStages, f.stageCalls)
	}
	s := r.RestStages[0]
	if s.API != "a1b2c3d4e5" || s.Stage != "prod" || !s.CacheEnabled || s.CacheSize != "6.1" || s.CacheStatus != "AVAILABLE" ||
		s.MethodCaching["*/*"] || !s.MethodCaching["~1pets/GET"] || s.Tags["team"] != "payments" {
		t.Errorf("stage: %+v", s)
	}
	// A stage's variables can hold credentials: never kept.
	if b, _ := json.Marshal(r.RestStages); strings.Contains(string(b), "hunter2") {
		t.Errorf("stage variables must never be read into costhound: %s", b)
	}
	// Metrics carry the name without non-ASCII characters, or the ID when nothing is left.
	if r.RestAPIMetricNames["orders"] != 2 || r.RestAPIMetricNames["Caf API"] != 1 || r.RestAPIMetricNames["p6q7r8s9t0"] != 1 {
		t.Errorf("metric names: %v", r.RestAPIMetricNames)
	}
	if len(skips) != 1 || skips[0] != "apigateway:GetStages" {
		t.Errorf("one denied API: one skip, the others still read: %v", skips)
	}
}
