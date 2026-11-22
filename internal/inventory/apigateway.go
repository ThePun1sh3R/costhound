package inventory

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
)

// APIGatewayAPI reads REST APIs and their stages. iam-policy.json grants
// apigateway:GET on /restapis and /restapis/*/stages only: never API keys
// or usage plan keys.
type APIGatewayAPI interface {
	apigateway.GetRestApisAPIClient
	GetStages(context.Context, *apigateway.GetStagesInput, ...func(*apigateway.Options)) (*apigateway.GetStagesOutput, error)
}

// RestStage is a REST API stage and its cache (HTTP and WebSocket APIs have
// none). Only these fields are kept: a stage's variables can hold
// credentials and are never copied.
type RestStage struct {
	API, APIName, Stage string
	CacheEnabled        bool
	CacheSize           string // GB as the API gives it: "0.5", "1.6", ... "237"
	CacheStatus         string // AVAILABLE, CREATE_IN_PROGRESS, FLUSH_IN_PROGRESS, ...
	// MethodCaching is each method setting's caching flag: "*/*" is the
	// stage default, "~1pets/GET" one method.
	MethodCaching map[string]bool
	Updated       *time.Time
	Tags          map[string]string
}

// TagRef is the stage as owner tags and findings name it.
func (s RestStage) TagRef() string { return "apigateway:restapis/" + s.API + "/stages/" + s.Stage }

// ARN is the stage's ARN (API Gateway ARNs carry no account).
func (s RestStage) ARN(region string) string {
	return "arn:aws:apigateway:" + region + "::/restapis/" + s.API + "/stages/" + s.Stage
}

func metricAPIName(name, id string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 127 {
			return -1
		}
		return r
	}, name)
	if ascii == "" {
		return id
	}
	return ascii
}

func loadRestStages(ctx context.Context, r *Region, c APIGatewayAPI, skip func(string, error)) {
	type api struct{ id, name string }
	var apis []api
	p := apigateway.NewGetRestApisPaginator(c, &apigateway.GetRestApisInput{Limit: aws.Int32(500)})
	if err := drain(ctx, p.HasMorePages, p.NextPage, func(o *apigateway.GetRestApisOutput) {
		for _, a := range o.Items {
			apis = append(apis, api{aws.ToString(a.Id), aws.ToString(a.Name)})
		}
	}); err != nil {
		skip("apigateway:GetRestApis", err)
		return
	}
	names := map[string]int{}
	stages := []RestStage{}
	failed := false
	for _, a := range apis {
		names[metricAPIName(a.name, a.id)]++
		out, err := c.GetStages(ctx, &apigateway.GetStagesInput{RestApiId: aws.String(a.id)})
		if err != nil {
			if !failed {
				skip("apigateway:GetStages", err)
				failed = true
			}
			continue // this API's stages stay unknown
		}
		for _, s := range out.Item {
			st := RestStage{API: a.id, APIName: a.name, Stage: aws.ToString(s.StageName), CacheEnabled: s.CacheClusterEnabled,
				CacheSize: string(s.CacheClusterSize), CacheStatus: string(s.CacheClusterStatus), Updated: s.LastUpdatedDate,
				MethodCaching: map[string]bool{}, Tags: s.Tags}
			for k, m := range s.MethodSettings {
				st.MethodCaching[k] = m.CachingEnabled
			}
			stages = append(stages, st)
		}
	}
	r.RestStages, r.RestAPIMetricNames = stages, names
}
