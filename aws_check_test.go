package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"

	"github.com/ThePun1sh3R/costhound/internal/license"
	"github.com/ThePun1sh3R/costhound/internal/runlog"
)

func TestAWSCheckProbeActionsInPolicyFiles(t *testing.T) {
	allowed := map[string]map[string]bool{}
	for _, name := range []string{"iam-policy.json", "iam-policy-pro-services.json"} {
		m := map[string]bool{}
		for _, st := range managedPolicy(t, name) {
			for _, a := range st.Action {
				m[a] = true
			}
		}
		allowed[name] = m
	}
	for _, p := range awsCheckProbeActions() {
		if !allowed[p.Policy][p.Action] {
			t.Errorf("probe %s names %s but that file does not allow it", p.Action, p.Policy)
		}
	}
}

func TestAWSCheckAllOK(t *testing.T) {
	cfg, _ := checkTestConfig(t, nil)
	rep, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Ready || rep.ExitCode() != 0 {
		t.Fatalf("ready=%v items=%+v", rep.Ready, rep.Items)
	}
	for _, it := range rep.Items {
		if it.Status == awsCheckFIX {
			t.Fatalf("unexpected FIX: %+v", it)
		}
	}
}

func TestAWSCheckOneDenied(t *testing.T) {
	deny := map[string]bool{"DescribeVolumes": true}
	cfg, _ := checkTestConfig(t, deny)
	rep, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ready || rep.ExitCode() != 1 {
		t.Fatalf("want exit 1, ready=%v", rep.Ready)
	}
	found := false
	for _, it := range rep.Items {
		if it.Name == "ec2" && it.Status == awsCheckFIX {
			found = true
			if !strings.Contains(it.Fix, "iam-policy.json") {
				t.Fatalf("fix=%q", it.Fix)
			}
		}
	}
	if !found {
		t.Fatalf("missing ec2 FIX: %+v", rep.Items)
	}
}

func TestAWSCheckLambdaOnlyDenied(t *testing.T) {
	deny := map[string]bool{"ListFunctions": true}
	cfg, _ := checkTestConfig(t, deny)
	rep, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Ready || rep.ExitCode() != 0 {
		t.Fatalf("lambda-only deny should stay ready, got ready=%v", rep.Ready)
	}
	for _, it := range rep.Items {
		if it.Name == "lambda" {
			if it.Status != awsCheckNOTE || !strings.Contains(it.Detail, "iam-policy-strict.json") {
				t.Fatalf("lambda: %+v", it)
			}
		}
	}
}

func TestAWSCheckSTSCredentialsFIX(t *testing.T) {
	deny := map[string]bool{"GetCallerIdentity": true}
	cfg, _ := checkTestConfig(t, deny)
	rep, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ready || len(rep.Items) != 1 || rep.Items[0].Name != "credentials" || rep.Items[0].Status != awsCheckFIX {
		t.Fatalf("want credentials FIX only: %+v", rep.Items)
	}
}

func TestAWSCheckNeverGetMetricDataOrCostExplorer(t *testing.T) {
	cfg, log := checkTestConfig(t, nil)
	_, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{Regions: "us-east-1", License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range log.Calls() {
		if c.Operation == "GetMetricData" {
			t.Fatal("GetMetricData must not run during -check")
		}
		if strings.EqualFold(c.Service, "Cost Explorer") || strings.HasPrefix(c.Operation, "GetCost") {
			t.Fatalf("Cost Explorer call: %+v", c)
		}
	}
}

func TestAWSCheckRunlogAttached(t *testing.T) {
	cfg, log := checkTestConfig(t, nil)
	_, err := awsCheck(context.Background(), cfg, license.PlanFree, AWSCheckOpts{Regions: "us-east-1", License: license.Status{Plan: license.PlanFree}})
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Calls()) == 0 {
		t.Fatal("run log must record probe calls")
	}
	if _, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{SecretId: aws.String("x")}); err == nil || !strings.Contains(err.Error(), "secret values") {
		t.Fatalf("runlog must refuse secret reads: %v", err)
	}
}

func TestAWSCheckSummaryText(t *testing.T) {
	rep := &AWSCheckReport{Items: []AWSCheckItem{{Status: awsCheckOK, Name: "ec2", Detail: "readable"}}}
	rep.Ready = true
	var buf bytes.Buffer
	if err := rep.Text(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Access is ready: run costhound") {
		t.Fatalf("summary: %q", buf.String())
	}
	rep.Items = append(rep.Items, AWSCheckItem{Status: awsCheckFIX, Name: "s3", Detail: "denied", Fix: "attach iam-policy.json"})
	rep.Ready = false
	buf.Reset()
	_ = rep.Text(&buf)
	if !strings.Contains(buf.String(), "fix: attach iam-policy.json") {
		t.Fatalf("fix line: %q", buf.String())
	}
}

func TestClassifyAWSProbeOrganizationsFIX(t *testing.T) {
	p := awsProbe{name: "organizations", action: "organizations:DescribeOrganization", policyFile: "iam-policy.json"}
	it := classifyAWSProbe(p, &smithy.GenericAPIError{Code: "AccessDenied"}, false)
	if it.Status != awsCheckFIX || !strings.Contains(it.Fix, "costhound-readonly-role.yaml") {
		t.Fatalf("%+v", it)
	}
}

func TestAppendAWSLicenseNoteFree(t *testing.T) {
	rep := &AWSCheckReport{Ready: true}
	appendAWSLicenseNote(rep, license.PlanFree, "")
	if len(rep.Items) != 1 || !strings.Contains(rep.Items[0].Detail, "plan free") {
		t.Fatalf("%+v", rep.Items)
	}
	if rep.ExitCode() != 0 {
		t.Fatal("license NOTE must keep exit 0")
	}
}

// checkTestConfig points every client at a local stub (no network) and denies
// the named operations (by operation name) with AccessDenied before anything
// is sent, whatever the service's wire protocol.
func checkTestConfig(t *testing.T, deny map[string]bool) (aws.Config, *runlog.Log) {
	t.Helper()
	srv := httptest.NewServer(checkStubHandler(t))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRetryMaxAttempts(1),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")))
	if err != nil {
		t.Fatal(err)
	}
	log := runlog.New()
	log.Attach(&cfg)
	cfg.APIOptions = append(cfg.APIOptions, func(st *middleware.Stack) error {
		return st.Initialize.Add(middleware.InitializeMiddlewareFunc("CheckTestDeny",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
				if deny[awsmiddleware.GetOperationName(ctx)] {
					return middleware.InitializeOutput{}, middleware.Metadata{}, &smithy.GenericAPIError{Code: "AccessDenied", Message: "denied"}
				}
				return next.HandleInitialize(ctx, in)
			}), middleware.After)
	})
	return cfg, log
}

// checkStubHandler answers STS and EC2 DescribeRegions like AWS does; other
// probes get an empty JSON body, which some protocols can't parse (a NOTE,
// never a FIX: only a denial is a FIX).
func checkStubHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := string(body) + r.URL.RawQuery
		switch {
		case strings.Contains(key, "Action=GetCallerIdentity"):
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>111122223333</Account><Arn>arn:aws:iam::111122223333:user/fixture</Arn></GetCallerIdentityResult></GetCallerIdentityResponse>`)
		case strings.Contains(key, "Action=DescribeRegions"):
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, `<DescribeRegionsResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><regionInfo><item><regionName>us-east-1</regionName></item></regionInfo></DescribeRegionsResponse>`)
		case strings.Contains(r.Header.Get("X-Amz-Target"), "GetEnrollmentStatus"):
			w.Header().Set("Content-Type", "application/x-amz-json-1.0")
			_, _ = io.WriteString(w, `{"status":"Active"}`)
		default:
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}
}

// awsCheckProbeActions lists every probe action and its policy file (for tests).
func awsCheckProbeActions() []struct{ Action, Policy string } {
	var out []struct{ Action, Policy string }
	for _, p := range append(append(awsCheckProbes(), awsCheckGlobalProbes()...), awsSpotProbe()) {
		out = append(out, struct{ Action, Policy string }{p.action, p.policyFile})
	}
	out = append(out, struct{ Action, Policy string }{"organizations:DescribeOrganization", "iam-policy.json"})
	return out
}

func TestClassifyAWSProbeSCPDenyIsNote(t *testing.T) {
	p := awsProbe{name: "compute-optimizer", action: "compute-optimizer:GetEnrollmentStatus", policyFile: "iam-policy.json"}
	scp := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:sts::123456789012:assumed-role/r/s is not authorized to perform: compute-optimizer:GetEnrollmentStatus on resource: * with an explicit deny in a service control policy: arn:aws:organizations::111111111111:policy/o-x/service_control_policy/p-x"}
	it := classifyAWSProbe(p, scp, true)
	if it.Status != awsCheckNOTE || it.Fix != "" || !strings.Contains(it.Detail, "service control policy") || !strings.Contains(it.Detail, "skipped") {
		t.Fatalf("SCP deny: %+v", it)
	}
	own := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: x is not authorized to perform: compute-optimizer:GetEnrollmentStatus because no identity-based policy allows the action"}
	if it := classifyAWSProbe(p, own, true); it.Status != awsCheckFIX || it.Fix != "attach iam-policy.json" {
		t.Fatalf("IAM deny: %+v", it)
	}
}
