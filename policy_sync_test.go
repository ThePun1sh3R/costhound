package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The StackSet role must allow exactly what iam-policy.json allows, so an
// Organization scan never hits a permission the single-account policy has.
func TestRoleTemplateMatchesPolicy(t *testing.T) {
	var pol struct {
		Statement []struct{ Action []string }
	}
	b, err := os.ReadFile("iam-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pol); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, st := range pol.Statement {
		want = append(want, st.Action...)
	}
	var got []string
	for _, st := range roleStatements(t) {
		got = append(got, st.Actions...)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("role template actions differ from iam-policy.json:\n got  %v\n want %v", got, want)
	}
	for _, a := range want {
		if a == "apigateway:GET" {
			continue // API Gateway's one read action: scoped to REST APIs and stages (TestAPIGatewayReadIsScopedToStages)
		}
		if !strings.Contains(a, ":Describe") && !strings.Contains(a, ":List") && !strings.Contains(a, ":Get") && !strings.Contains(a, ":BatchGet") {
			t.Errorf("%s is not a read-only action", a)
		}
	}
	for _, bad := range []string{"s3:GetObject", "secretsmanager:GetSecretValue", "logs:GetLogEvents"} {
		for _, a := range want {
			if a == bad {
				t.Errorf("%s reads customer data: costhound must not need it", bad)
			}
		}
	}
}

type roleStatement struct {
	Actions   []string
	Resources []string // "*", or the ARNs the scoped API Gateway statement names
	CalledVia bool     // conditioned on aws:CalledVia ec2-images.amazonaws.com
}

var actionName = regexp.MustCompile(`^[a-z0-9-]+:[A-Za-z0-9]+$`) // exact names: no wildcards

// roleStatements parses the StackSet template's policy the way CloudFormation
// will (CloudFormation tags such as !Ref stay plain nodes) and fails on any
// statement that isn't an Allow on "*" with a non-empty list of exact actions.
func roleStatements(t *testing.T) []roleStatement {
	t.Helper()
	b, err := os.ReadFile("deploy/costhound-readonly-role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	get := func(n *yaml.Node, key string) *yaml.Node {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	n := get(get(get(get(doc.Content[0], "Resources"), "CosthoundReadOnlyRole"), "Properties"), "Policies")
	if n == nil || n.Kind != yaml.SequenceNode || len(n.Content) != 1 {
		t.Fatal("want exactly one inline policy")
	}
	sts := get(get(n.Content[0], "PolicyDocument"), "Statement")
	if sts == nil || sts.Kind != yaml.SequenceNode {
		t.Fatal("no Statement list")
	}
	var out []roleStatement
	for i, st := range sts.Content {
		if st.Kind != yaml.MappingNode {
			t.Fatalf("statement %d is a %v, not a mapping (an Action list indented at the statement level parses like this)", i, st.Tag)
		}
		e, res := get(st, "Effect"), resourceValues(get(st, "Resource"))
		if e == nil || e.Value != "Allow" || len(res) == 0 {
			t.Errorf("statement %d: want Effect Allow on Resource * (or the scoped API Gateway ARNs)", i)
		}
		act := get(st, "Action")
		if act == nil || act.Kind != yaml.SequenceNode || len(act.Content) == 0 {
			t.Fatalf("statement %d: Action must be a non-empty list", i)
		}
		rs := roleStatement{Resources: res}
		for _, a := range act.Content {
			if a.Kind != yaml.ScalarNode || !actionName.MatchString(a.Value) {
				t.Errorf("statement %d: %q is not an exact action name", i, a.Value)
			}
			rs.Actions = append(rs.Actions, a.Value)
		}
		for _, op := range condition(get(st, "Condition")) {
			rs.CalledVia = rs.CalledVia || op
		}
		out = append(out, rs)
	}
	return out
}

// condition reports, per condition operator, whether it requires aws:CalledVia
// to be ec2-images.amazonaws.com.
func condition(n *yaml.Node) []bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out []bool
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys := n.Content[i+1]
		via := false
		for j := 0; keys.Kind == yaml.MappingNode && j+1 < len(keys.Content); j += 2 {
			if keys.Content[j].Value != "aws:CalledVia" {
				continue
			}
			v := keys.Content[j+1]
			vals := []*yaml.Node{v}
			if v.Kind == yaml.SequenceNode {
				vals = v.Content
			}
			for _, x := range vals {
				via = via || x.Value == "ec2-images.amazonaws.com"
			}
		}
		out = append(out, via)
	}
	return out
}

// Permissions that return user data or parameter values may only be granted
// through EC2's AMI reference check (aws:CalledVia), never to costhound
// itself: checked statement by statement in both the policy and the template.
func TestNoPermissionReturnsUserDataOrParameterValues(t *testing.T) {
	sensitive := map[string]bool{"ec2:DescribeLaunchTemplateVersions": true, "ec2:DescribeInstanceAttribute": true,
		"autoscaling:DescribeLaunchConfigurations": true, "ssm:GetParameter": true, "ssm:GetParameters": true, "ssm:GetParametersByPath": true}
	var pol struct {
		Statement []struct {
			Action    []string
			Condition map[string]map[string]any
		}
	}
	b, err := os.ReadFile("iam-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pol); err != nil {
		t.Fatal(err)
	}
	viaJSON, viaYAML := map[string]bool{}, map[string]bool{}
	for _, st := range pol.Statement {
		via := false
		for _, kv := range st.Condition {
			if v, ok := kv["aws:CalledVia"]; ok && strings.Contains(fmt.Sprint(v), "ec2-images.amazonaws.com") {
				via = true
			}
		}
		for _, a := range st.Action {
			if sensitive[a] && !via {
				t.Errorf("iam-policy.json grants %s unconditionally: it returns user data or parameter values", a)
			}
			if via {
				viaJSON[a] = true
			}
		}
	}
	for i, st := range roleStatements(t) {
		for _, a := range st.Actions {
			if sensitive[a] && !st.CalledVia {
				t.Errorf("role template statement %d grants %s without aws:CalledVia ec2-images.amazonaws.com", i, a)
			}
			if st.CalledVia {
				viaYAML[a] = true
			}
		}
	}
	if fmt.Sprint(viaJSON) != fmt.Sprint(viaYAML) {
		t.Errorf("the conditioned actions differ: policy %v, template %v", viaJSON, viaYAML)
	}
}

// EC2 returns each Site-to-Site VPN tunnel's pre-shared key from these (the
// tunnel options and the customer gateway configuration), so no statement
// may grant them, conditioned or not: the VPN check reads CloudWatch only.
func TestNoPermissionReturnsVPNKeys(t *testing.T) {
	never := map[string]bool{"ec2:DescribeVpnConnections": true, "ec2:GetVpnConnectionDeviceSampleConfiguration": true}
	var pol struct{ Statement []struct{ Action []string } }
	b, err := os.ReadFile("iam-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pol); err != nil {
		t.Fatal(err)
	}
	for i, st := range pol.Statement {
		for _, a := range st.Action {
			if never[a] {
				t.Errorf("iam-policy.json statement %d grants %s: it returns VPN tunnel keys", i, a)
			}
		}
	}
	for i, st := range roleStatements(t) {
		for _, a := range st.Actions {
			if never[a] {
				t.Errorf("role template statement %d grants %s: it returns VPN tunnel keys", i, a)
			}
		}
	}
}

// resourceValues reads a statement's Resource: "*" or a list of ARNs
// (CloudFormation !Sub nodes keep their text).
func resourceValues(n *yaml.Node) []string {
	switch {
	case n == nil:
		return nil
	case n.Kind == yaml.ScalarNode:
		return []string{n.Value}
	case n.Kind == yaml.SequenceNode:
		var out []string
		for _, x := range n.Content {
			if x.Kind != yaml.ScalarNode {
				return nil
			}
			out = append(out, x.Value)
		}
		return out
	}
	return nil
}

// iamMatch matches an ARN to an IAM resource pattern: * is any run of
// characters, "/" included; ? is one character.
func iamMatch(pattern, arn string) bool {
	re := "^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(pattern)) + "$"
	return regexp.MustCompile(re).MatchString(arn)
}

// apigateway:GET is API Gateway's one read action, and on /apikeys it can
// return API key values: costhound gets it on REST APIs and their stages
// only, in a statement of its own, in both the policy and the StackSet
// template. Every other statement stays on Resource "*".
func TestAPIGatewayReadIsScopedToStages(t *testing.T) {
	var pol struct {
		Statement []struct {
			Action   []string
			Resource any
		}
	}
	b, err := os.ReadFile("iam-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pol); err != nil {
		t.Fatal(err)
	}
	want := []string{"arn:*:apigateway:*::/restapis", "arn:*:apigateway:*::/restapis/*/stages"}
	// Never: API keys, usage plan keys, client certificates, a stage's
	// exported definition or generated SDKs.
	banned := []string{"arn:aws:apigateway:eu-west-1::/apikeys", "arn:aws:apigateway:eu-west-1::/apikeys/k1a2b3c4d5",
		"arn:aws:apigateway:eu-west-1::/usageplans/u1a2b3/keys", "arn:aws:apigateway:eu-west-1::/usageplans/u1a2b3/keys/k1a2b3c4d5",
		"arn:aws:apigateway:eu-west-1::/clientcertificates", "arn:aws:apigateway:eu-west-1::/clientcertificates/c1a2b3",
		"arn:aws:apigateway:eu-west-1::/restapis/a1b2c3d4e5/stages/prod/exports/swagger",
		"arn:aws:apigateway:eu-west-1::/restapis/a1b2c3d4e5/stages/prod/sdks/javascript"}
	// The list must catch a widening such as /restapis/*/stages*.
	if !iamMatch("arn:*:apigateway:*::/restapis/*/stages*", banned[6]) || !iamMatch("arn:*:apigateway:*::/*", banned[4]) {
		t.Fatal("the banned list no longer catches a widened resource")
	}
	scoped := 0
	check := func(where string, actions, resources []string) {
		get := false
		for _, a := range actions {
			get = get || a == "apigateway:GET" || a == "apigateway:*" || a == "*"
		}
		if !get {
			if len(resources) != 1 || resources[0] != "*" {
				t.Errorf("%s: only the API Gateway statement may name resources: %v", where, resources)
			}
			return
		}
		scoped++
		if len(actions) != 1 || actions[0] != "apigateway:GET" || strings.Join(resources, " ") != strings.Join(want, " ") {
			t.Errorf("%s: want apigateway:GET alone, on %v; got %v on %v", where, want, actions, resources)
		}
		for _, arn := range banned {
			for _, r := range resources {
				if iamMatch(r, arn) {
					t.Errorf("%s: %s reaches %s, where API key values are", where, r, arn)
				}
			}
		}
		for _, arn := range []string{"arn:aws:apigateway:eu-west-1::/restapis", "arn:aws:apigateway:eu-west-1::/restapis/a1b2c3d4e5/stages"} {
			ok := false
			for _, r := range resources {
				ok = ok || iamMatch(r, arn)
			}
			if !ok {
				t.Errorf("%s: nothing allows %s", where, arn)
			}
		}
	}
	for i, st := range pol.Statement {
		var res []string
		switch v := st.Resource.(type) {
		case string:
			res = []string{v}
		case []any:
			for _, x := range v {
				s, _ := x.(string)
				res = append(res, s)
			}
		}
		check(fmt.Sprintf("iam-policy.json statement %d", i), st.Action, res)
	}
	for i, st := range roleStatements(t) {
		res := make([]string, len(st.Resources))
		for j, r := range st.Resources {
			res[j] = strings.ReplaceAll(r, "${AWS::Partition}", "*")
		}
		check(fmt.Sprintf("role template statement %d", i), st.Actions, res)
	}
	if scoped != 2 {
		t.Errorf("want the scoped API Gateway statement once in each file, found %d", scoped)
	}
}

// secretCapableReads answer with settings where people sometimes paste
// secrets: Lambda environment variables, EMR configurations and step
// arguments, ECS container environments, Lambda event source filters (in
// plain text unless the customer's own KMS key encrypts them) and an ECS
// Service Connect log driver's options (a Splunk token), and a Bedrock
// knowledge base's storage settings (a Pinecone connection string, database
// users; costhound keeps only the ID, the name and an OpenSearch Serverless
// collection's ARN), DMS instance Kerberos configuration, task and
// replication settings and failure details, and Transfer server custom
// identity provider URLs and login banners. API Gateway's stage
// reads (stage variables) are the other one,
// left out by resource. costhound drops those fields on arrival; the strict
// policy keeps the answers from reaching it at all.
var secretCapableReads = []string{"lambda:ListFunctions", "elasticmapreduce:DescribeCluster", "elasticmapreduce:ListSteps", "ecs:DescribeTaskDefinition",
	"lambda:ListEventSourceMappings", "ecs:DescribeServices", "bedrock:GetKnowledgeBase",
	"dms:DescribeReplicationInstances", "dms:DescribeReplicationTasks", "dms:DescribeReplicationConfigs", "dms:DescribeReplications", "transfer:DescribeServer"}

// iam-policy-strict.json is iam-policy.json without the secret-capable reads:
// the same statements in the same order, nothing added, and API Gateway left
// with listing APIs (not their stages).
func TestStrictPolicyLeavesOutSecretCapableReads(t *testing.T) {
	type stmt struct {
		Sid       string
		Action    []string
		Resource  any
		Condition any
	}
	load := func(name string) []stmt {
		var p struct{ Statement []stmt }
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p.Statement
	}
	full, strict := load("iam-policy.json"), load("iam-policy-strict.json")
	if len(full) != len(strict) {
		t.Fatalf("%d statements in the full policy, %d in the strict one", len(full), len(strict))
	}
	leave := map[string]bool{}
	for _, a := range secretCapableReads {
		leave[a] = true
	}
	seen := map[string]bool{}
	for i := range full {
		var want []string
		for _, a := range full[i].Action {
			if leave[a] {
				seen[a] = true
				continue
			}
			want = append(want, a)
		}
		if strings.Join(strict[i].Action, ",") != strings.Join(want, ",") {
			t.Errorf("statement %d: strict actions %v, want %v", i, strict[i].Action, want)
		}
		fr, sr := fmt.Sprint(full[i].Resource), fmt.Sprint(strict[i].Resource)
		if slices.Contains(full[i].Action, "apigateway:GET") {
			if strings.Contains(sr, "/stages") || !strings.Contains(sr, "/restapis") {
				t.Errorf("strict API Gateway may list APIs but never read stages: %s", sr)
			}
		} else if fr != sr {
			t.Errorf("statement %d: resources differ: %s vs %s", i, sr, fr)
		}
		if fmt.Sprint(full[i].Condition) != fmt.Sprint(strict[i].Condition) {
			t.Errorf("statement %d: conditions differ", i)
		}
	}
	readme, _ := os.ReadFile("README.md")
	for _, a := range append(secretCapableReads, "apigateway:GetStages") {
		if !seen[a] && a != "apigateway:GetStages" {
			t.Errorf("%s isn't in iam-policy.json any more: take it off secretCapableReads", a)
		}
		if op := a[strings.Index(a, ":")+1:]; !strings.Contains(string(readme), op) {
			t.Errorf("README must name %s among the calls that can answer with secrets", op)
		}
	}
}

// -creators reads CloudTrail's event history, an audit log: its one
// permission lives in iam-policy-creators.json and in neither other policy.
func TestCreatorsPolicyStandsAlone(t *testing.T) {
	actions := func(name string) []string {
		var p struct{ Statement []struct{ Action []string } }
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, st := range p.Statement {
			out = append(out, st.Action...)
		}
		return out
	}
	if got := actions("iam-policy-creators.json"); strings.Join(got, ",") != "cloudtrail:LookupEvents" {
		t.Errorf("iam-policy-creators.json: %v", got)
	}
	for _, name := range []string{"iam-policy.json", "iam-policy-strict.json"} {
		if slices.Contains(actions(name), "cloudtrail:LookupEvents") {
			t.Errorf("%s must not grant cloudtrail:LookupEvents", name)
		}
	}
}
