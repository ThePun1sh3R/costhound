package owner

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	rgt "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func TestPick(t *testing.T) {
	def := NewKeys(nil)
	for _, c := range []struct {
		keys       Keys
		tags       map[string]string
		owner, frm string
	}{
		{def, map[string]string{"Team": "payments", "Name": "web"}, "payments", "tag Team"},
		{def, map[string]string{"team": "a", "Owner": "b"}, "b", "tag Owner"}, // owner before team
		{def, map[string]string{"created_by": "alice"}, "alice", "tag created_by"},
		{def, map[string]string{"CreatedBy": "alice"}, "alice", "tag CreatedBy"},
		{def, map[string]string{"owner": "  ", "contact": "ops@example.com"}, "ops@example.com", "tag contact"},
		{def, map[string]string{"owner": "a", "Owner": "b"}, "b", "tag Owner"}, // two spellings: alphabetical
		{NewKeys([]string{"Squad"}), map[string]string{"Owner": "b", "squad": "s"}, "s", "tag squad"},
		{def, map[string]string{"aws:cloudformation:stack-name": "payments-prod", "Name": "x"}, "payments-prod", "CloudFormation stack"},
		{def, map[string]string{"owner": "o", "aws:cloudformation:stack-name": "s"}, "o", "tag owner"},
		{def, map[string]string{"kubernetes.io/created-for/pvc/namespace": "billing"}, "billing", "Kubernetes namespace"},
		{def, map[string]string{"Name": "web"}, "", None},
		{def, nil, "", None},
		{def, map[string]string{"owner": "ev\x1b[31mil\n"}, "ev[31mil", "tag owner"},
	} {
		o, f := c.keys.Pick(c.tags)
		if o != c.owner || f != c.frm {
			t.Errorf("Pick(%v) = %q, %q; want %q, %q", c.tags, o, f, c.owner, c.frm)
		}
	}
	if o, _ := def.Pick(map[string]string{"owner": strings.Repeat("x", 300)}); len(o) != 131 {
		t.Errorf("long owners are cut to 128 characters: got %d", len(o))
	}
}

// Every resource type a check names (internal/checks/owners.go), in the ARN
// form AWS returns from tag:GetResources, must come out as the check's ref.
func TestRefMatchesTheChecks(t *testing.T) {
	for arn, want := range map[string]string{
		"arn:aws:ec2:eu-west-1:123456789012:volume/vol-0abc":                                       "ec2:volume/vol-0abc",
		"arn:aws:apigateway:eu-west-1::/restapis/a1b2c3d4e5/stages/prod":                           "apigateway:restapis/a1b2c3d4e5/stages/prod",
		"arn:aws:ec2:eu-west-1:123456789012:instance/i-0abc":                                       "ec2:instance/i-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:elastic-ip/eipalloc-0abc":                              "ec2:elastic-ip/eipalloc-0abc",
		"arn:aws:ec2:eu-west-1::snapshot/snap-0abc":                                                "ec2:snapshot/snap-0abc",
		"arn:aws:ec2:eu-west-1::image/ami-0abc":                                                    "ec2:image/ami-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:natgateway/nat-0abc":                                   "ec2:natgateway/nat-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-0abc":                                          "ec2:vpc/vpc-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:vpc-endpoint/vpce-0abc":                                "ec2:vpc-endpoint/vpce-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:dedicated-host/h-0abc":                                 "ec2:dedicated-host/h-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:transit-gateway-attachment/tgw-attach-0abc":            "ec2:transit-gateway-attachment/tgw-attach-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:client-vpn-endpoint/cvpn-endpoint-0abc":                "ec2:client-vpn-endpoint/cvpn-endpoint-0abc",
		"arn:aws:ec2:eu-west-1:123456789012:vpn-connection/vpn-0abc":                               "ec2:vpn-connection/vpn-0abc",
		"arn:aws:logs:eu-west-1:123456789012:log-group:/aws/lambda/web:*":                          "logs:log-group:/aws/lambda/web",
		"arn:aws:logs:eu-west-1:123456789012:log-group:/aws/lambda/web":                            "logs:log-group:/aws/lambda/web",
		"arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/web/50dc6c495":       "elasticloadbalancing:loadbalancer/app/web/50dc6c495",
		"arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-pass-AbCd12":                 "secretsmanager:secret:prod/db-pass",
		"arn:aws:globalaccelerator::123456789012:accelerator/1234abcd-abcd-1234-abcd-1234abcdefgh": "globalaccelerator:accelerator/1234abcd-abcd-1234-abcd-1234abcdefgh",
		"arn:aws:ecr:eu-west-1:123456789012:repository/team/app":                                   "ecr:repository/team/app",
		"arn:aws:dynamodb:eu-west-1:123456789012:table/orders":                                     "dynamodb:table/orders",
		"arn:aws:elasticfilesystem:eu-west-1:123456789012:file-system/fs-0abc":                     "elasticfilesystem:file-system/fs-0abc",
		"arn:aws:kms:eu-west-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab":              "kms:key/1234abcd-12ab-34cd-56ef-1234567890ab",
		"arn:aws:eks:eu-west-1:123456789012:cluster/prod":                                          "eks:cluster/prod",
		"arn:aws:rds:eu-west-1:123456789012:db:orders-db":                                          "rds:db:orders-db",
		"arn:aws:kinesis:eu-west-1:123456789012:stream/clicks":                                     "kinesis:stream/clicks",
		"arn:aws:es:eu-west-1:123456789012:domain/search":                                          "es:domain/search",
		"arn:aws:sagemaker:eu-west-1:123456789012:endpoint/churn-model":                            "sagemaker:endpoint/churn-model",
		"arn:aws:redshift:eu-west-1:123456789012:cluster:warehouse":                                "redshift:cluster:warehouse",
		"arn:aws:elasticache:eu-west-1:123456789012:cluster:sessions":                              "elasticache:cluster:sessions",
		"arn:aws:elasticache:eu-west-1:123456789012:replicationgroup:sessions":                     "elasticache:replicationgroup:sessions",
		"arn:aws:kafka:eu-west-1:123456789012:cluster/events/0d2f-4a1c-1":                          "kafka:cluster/events/0d2f-4a1c-1",
		"arn:aws-us-gov:ec2:us-gov-west-1:123456789012:volume/vol-0abc":                            "ec2:volume/vol-0abc",
		"arn:aws:s3:::logs-archive":                                                                "s3:logs-archive",
		"":                                                                                         "", "vol-0abc": "", "arn:aws:ec2": "",
	} {
		if got := Ref(arn); got != want {
			t.Errorf("Ref(%q) = %q, want %q", arn, got, want)
		}
	}
	// A finding names a SageMaker endpoint as written; its ARN is lower case.
	if normRef("sagemaker:endpoint/Churn-Model") != Ref("arn:aws:sagemaker:eu-west-1:1:endpoint/churn-model") {
		t.Error("SageMaker refs must match whatever the case")
	}
	// A secret whose own name ends like a suffix keeps it in the finding's ref.
	if Ref("arn:aws:secretsmanager:eu-west-1:1:secret:app-prod12-AbCd12") != "secretsmanager:secret:app-prod12" {
		t.Error("only the ARN's random suffix goes")
	}
}

func TestType(t *testing.T) {
	for ref, want := range map[string]string{
		"ec2:volume/vol-1": "ec2:volume", "logs:log-group:/a/b": "logs:log-group", "redshift:cluster:w": "redshift:cluster",
		"elasticache:replicationgroup:s": "elasticache:replicationgroup", "secretsmanager:secret:x": "secretsmanager:secret",
		"kafka:cluster/a/b": "kafka:cluster", "elasticloadbalancing:loadbalancer/app/x/1": "elasticloadbalancing:loadbalancer",
		"wafv2:regional/webacl/example/id": "wafv2", "wafv2:global/webacl/example/id": "wafv2",
		"x": "", ":a/b": "", "s3:bucket": "s3", "s3:": "",
	} {
		if got := Type(ref); got != want {
			t.Errorf("Type(%q) = %q, want %q", ref, got, want)
		}
	}
}

// fakeTagging serves tag:GetResources from a map of type -> pages.
type fakeTagging struct {
	pages map[string][][]rgttypes.ResourceTagMapping
	fail  map[string]error
	calls []string
}

func (f *fakeTagging) GetResources(_ context.Context, in *rgt.GetResourcesInput, _ ...func(*rgt.Options)) (*rgt.GetResourcesOutput, error) {
	typ := in.ResourceTypeFilters[0]
	f.calls = append(f.calls, typ)
	if len(in.ResourceTypeFilters) != 1 || aws.ToInt32(in.ResourcesPerPage) != 100 {
		return nil, &smithy.GenericAPIError{Code: "InvalidParameterException"}
	}
	if err := f.fail[typ]; err != nil {
		return nil, err
	}
	i := 0
	if tok := aws.ToString(in.PaginationToken); tok != "" {
		i = int(tok[0] - '0')
	}
	out := &rgt.GetResourcesOutput{}
	if pages := f.pages[typ]; i < len(pages) {
		out.ResourceTagMappingList = pages[i]
		if i+1 < len(pages) {
			out.PaginationToken = aws.String(string(rune('0' + i + 1)))
		}
	}
	return out, nil
}

func mapping(arn string, kv ...string) rgttypes.ResourceTagMapping {
	m := rgttypes.ResourceTagMapping{ResourceARN: aws.String(arn)}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Tags = append(m.Tags, rgttypes.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}
	return m
}

func ec2tag(k, v string) ec2types.Tag { return ec2types.Tag{Key: aws.String(k), Value: aws.String(v)} }

func TestAttach(t *testing.T) {
	r := &inventory.Region{
		Name: "eu-west-1",
		Volumes: []ec2types.Volume{
			{VolumeId: aws.String("vol-1"), Tags: []ec2types.Tag{ec2tag("Team", "payments"), ec2tag("Name", "data")}},
			{VolumeId: aws.String("vol-2")},
		},
		Secrets: []smtypes.SecretListEntry{{Name: aws.String("prod/db"), Tags: []smtypes.Tag{{Key: aws.String("owner"), Value: aws.String("dba")}}}},
	}
	api := &fakeTagging{
		pages: map[string][][]rgttypes.ResourceTagMapping{
			"dynamodb:table": {
				{mapping("arn:aws:dynamodb:eu-west-1:1:table/orders", "owner", "data-eng", "api-token", "never-kept")},
				{mapping("arn:aws:dynamodb:eu-west-1:1:table/cleared")}, // tagged once, no tags now
			},
		},
		fail: map[string]error{"logs:log-group": &smithy.GenericAPIError{Code: "InvalidParameterException"}},
	}
	fs := []checks.Finding{
		{Check: "unattached-ebs", TagRef: "ec2:volume/vol-1"},
		{Check: "unattached-ebs", TagRef: "ec2:volume/vol-2"},
		{Check: "unused-secret", TagRef: "secretsmanager:secret:prod/db"},
		{Check: "idle-dynamodb", TagRef: "dynamodb:table/orders"},
		{Check: "idle-dynamodb", TagRef: "dynamodb:table/cleared"},
		{Check: "idle-dynamodb", TagRef: "dynamodb:table/untagged"},
		{Check: "log-retention", TagRef: "logs:log-group:/app"},
		{Check: "lambda-x86"},
	}
	Attach(context.Background(), r, fs, api, NewKeys(nil))
	want := [][2]string{
		{"payments", "tag Team"}, {"", None}, {"dba", "tag owner"},
		{"data-eng", "tag owner"}, {"", None}, {"", None},
		{"", ""}, // its tags couldn't be read: unknown, not unowned
		{"", ""}, // about several resources
	}
	for i, w := range want {
		if fs[i].Owner != w[0] || fs[i].OwnerFrom != w[1] {
			t.Errorf("%s %s: owner %q from %q, want %q from %q", fs[i].Check, fs[i].TagRef, fs[i].Owner, fs[i].OwnerFrom, w[0], w[1])
		}
	}
	if strings.Join(api.calls, " ") != "dynamodb:table dynamodb:table logs:log-group" {
		t.Errorf("only types with findings and no tags in hand are read, every page: %v", api.calls)
	}
	if len(r.Skipped) != 1 || r.Skipped[0].What != "tag:GetResources" || !strings.Contains(r.Skipped[0].Error, "logs:log-group") {
		t.Errorf("skipped = %+v", r.Skipped)
	}
}

func TestAttachDeniedStopsAtTheFirstType(t *testing.T) {
	r := &inventory.Region{Name: "eu-west-1"}
	api := &fakeTagging{fail: map[string]error{
		"dynamodb:table": &smithy.GenericAPIError{Code: "AccessDeniedException"},
		"kinesis:stream": &smithy.GenericAPIError{Code: "AccessDeniedException"},
	}}
	fs := []checks.Finding{{TagRef: "kinesis:stream/a"}, {TagRef: "dynamodb:table/b"}}
	Attach(context.Background(), r, fs, api, NewKeys(nil))
	if len(api.calls) != 1 || len(r.Skipped) != 1 || r.Skipped[0].Error != "AccessDeniedException" {
		t.Errorf("denied once, one skip: calls %v, skipped %+v", api.calls, r.Skipped)
	}
	if fs[0].OwnerFrom != "" || fs[1].OwnerFrom != "" {
		t.Errorf("unread tags must leave the owner unknown: %+v", fs)
	}
}

func TestOnlyOwnerTagsAreKept(t *testing.T) {
	api := &fakeTagging{pages: map[string][][]rgttypes.ResourceTagMapping{
		"dynamodb:table": {{mapping("arn:aws:dynamodb:eu-west-1:1:table/a", "Owner", "x", "db-password", "hunter2", "aws:cloudformation:stack-name", "s")}},
	}}
	got := map[string]map[string]string{}
	if err := NewKeys(nil).sweep(context.Background(), api, "dynamodb:table", got); err != nil {
		t.Fatal(err)
	}
	tags := got["dynamodb:table/a"]
	if len(tags) != 2 || tags["Owner"] != "x" || tags["aws:cloudformation:stack-name"] != "s" {
		t.Errorf("kept %v: only owner and stack tags may stay in memory", tags)
	}
}

func TestAliases(t *testing.T) {
	ks := NewKeys(nil).WithAliases(map[string][]string{"payments": {"Pay-Team", "payments@example.com", "payments-prod"}})
	for tags, want := range map[string]string{
		"pay-team": "payments", "PAYMENTS@example.com": "payments", "Payments": "payments", "data": "data",
	} {
		if got, _ := ks.Pick(map[string]string{"team": tags}); got != want {
			t.Errorf("team=%s reports as %q, want %q", tags, got, want)
		}
	}
	if got, from := ks.Pick(map[string]string{"aws:cloudformation:stack-name": "payments-prod"}); got != "payments" || from != "CloudFormation stack" {
		t.Errorf("a stack can be an alias too: %q from %q", got, from)
	}
}

func TestKubernetesClusterOwner(t *testing.T) {
	ks := NewKeys(nil)
	if o, f := ks.Pick(map[string]string{"kubernetes.io/cluster/prod": "owned"}); o != "Kubernetes cluster prod" || f != "Kubernetes cluster tag" {
		t.Errorf("owned: %q %q", o, f)
	}
	if o, _ := ks.Pick(map[string]string{"kubernetes.io/cluster/prod": "shared"}); o != "" {
		t.Errorf("shared means the user's resource, lent to the cluster: %q", o)
	}
	if o, _ := ks.Pick(map[string]string{"kubernetes.io/cluster/prod": "owned", "team": "payments"}); o != "payments" {
		t.Errorf("an owner tag comes first: %q", o)
	}
	if !ks.relevant("kubernetes.io/cluster/prod") || !ks.relevant("eks:eks-cluster-name") {
		t.Error("the cluster tags must be kept for the lookup")
	}
}

// A REST API stage's tags come with GetStages: its owner needs no
// tag:GetResources call, and the ref is the one the findings carry.
func TestStageOwnerFromItsOwnTags(t *testing.T) {
	st := inventory.RestStage{API: "a1b2c3d4e5", APIName: "orders", Stage: "prod", Tags: map[string]string{"team": "payments", "cost-center": "x"}}
	r := &inventory.Region{Name: "eu-west-1", RestStages: []inventory.RestStage{st}}
	fs := []checks.Finding{{Check: "api-cache-unused", TagRef: st.TagRef()}}
	Attach(context.Background(), r, fs, &fakeTagging{fail: map[string]error{"apigateway:restapis": &smithy.GenericAPIError{Code: "NotCalled"}}}, NewKeys(nil))
	if fs[0].Owner != "payments" || fs[0].OwnerFrom != "tag team" {
		t.Errorf("owner from the stage's own tags: %q %q, skipped %v", fs[0].Owner, fs[0].OwnerFrom, r.Skipped)
	}
	if Ref(st.ARN("eu-west-1")) != st.TagRef() {
		t.Errorf("the stage ARN's ref %q must be the findings' ref %q", Ref(st.ARN("eu-west-1")), st.TagRef())
	}
}

// An accelerator's tags come with ListTagsForResource (us-west-2): its owner
// needs no tag:GetResources, whatever region the scan's tagging client is
// in. Unread tags and no tagger: no owner, and not "none" either.
func TestAcceleratorOwnerFromItsOwnTags(t *testing.T) {
	a := inventory.Accelerator{ID: "1234abcd-abcd-1234-abcd-1234abcdefgh", Tags: map[string]string{"team": "edge"}}
	r := &inventory.Region{Name: "global", Accelerators: []inventory.Accelerator{a}}
	fs := []checks.Finding{{Check: "idle-accelerator", TagRef: a.TagRef()}}
	Attach(context.Background(), r, fs, nil, NewKeys(nil))
	if fs[0].Owner != "edge" || fs[0].OwnerFrom != "tag team" {
		t.Errorf("owner from its own tags: %+v", fs[0])
	}
	if Ref("arn:aws:globalaccelerator::123456789012:accelerator/"+a.ID) != a.TagRef() {
		t.Errorf("the ARN's ref must be the findings' ref %q", a.TagRef())
	}
	a.Tags = nil
	r.Accelerators = []inventory.Accelerator{a}
	fs = []checks.Finding{{Check: "idle-accelerator", TagRef: a.TagRef()}}
	Attach(context.Background(), r, fs, nil, NewKeys(nil))
	if fs[0].Owner != "" || fs[0].OwnerFrom != "" {
		t.Errorf("tags unread and no tagger: unknown, not untagged: %+v", fs[0])
	}
}
