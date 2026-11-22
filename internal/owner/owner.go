// Package owner says who to ask about a finding: the owner tag on its
// resource (owner, team, created-by or contact, or keys a team lists in
// .costhound.yaml), else the CloudFormation stack or Kubernetes namespace
// that made it.
//
// Tags come from the scan's own describe calls where those return them (EC2,
// Secrets Manager), and from the Resource Groups Tagging API (tag:GetResources)
// for the rest: one resource type at a time, only for types that have
// findings. Only the owner tags are kept; every other tag value is dropped as
// soon as it is read.
package owner

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	rgt "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/smithy-go"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// None is OwnerFrom for a resource that has no owner tag.
const None = "none"

// DefaultKeys are the owner tag keys tried after a team's own. Keys match
// without regard to case, spaces, '-', '_', '.' or ':' ("Owner", "created_by").
var DefaultKeys = []string{"owner", "team", "created-by", "contact"}

const (
	stackKey     = "aws:cloudformation:stack-name"
	namespaceKey = "kubernetes.io/created-for/pvc/namespace" // EBS volumes made for a Kubernetes claim
)

// Keys are the owner tag keys, most trusted first, and the names a team
// merges (owners: in .costhound.yaml).
type Keys struct {
	order []string
	alias map[string]string // lower-case name or alias -> the name to report
}

// WithAliases reports each alias as its owner's name ("pay-team",
// "payments@example.com" -> "payments"); the name itself matches in any case.
func (ks Keys) WithAliases(owners map[string][]string) Keys {
	ks.alias = map[string]string{}
	for name, aliases := range owners {
		ks.alias[strings.ToLower(name)] = name
		for _, a := range aliases {
			ks.alias[strings.ToLower(strings.TrimSpace(a))] = name
		}
	}
	return ks
}

func (ks Keys) named(v string) string {
	if n, ok := ks.alias[strings.ToLower(v)]; ok {
		return n
	}
	return v
}

// NewKeys puts a team's keys first, then the defaults.
func NewKeys(extra []string) Keys {
	var ks Keys
	seen := map[string]bool{}
	for _, k := range append(append([]string{}, extra...), DefaultKeys...) {
		if n := norm(k); n != "" && !seen[n] {
			seen[n] = true
			ks.order = append(ks.order, n)
		}
	}
	return ks
}

func norm(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		switch r {
		case ' ', '-', '_', '.', ':':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// relevant: a tag the lookup reads. Everything else is dropped unread.
func (ks Keys) relevant(k string) bool {
	if k == stackKey || k == namespaceKey || k == "KubernetesCluster" || k == "eks:eks-cluster-name" || strings.HasPrefix(k, "kubernetes.io/cluster/") {
		return true
	}
	n := norm(k)
	for _, o := range ks.order {
		if n == o {
			return true
		}
	}
	return false
}

// Pick says who owns a resource with these tags, and where that came from:
// its first owner key in Keys order (between two spellings of one key, the
// alphabetically first), else its CloudFormation stack, else its Kubernetes
// namespace. With none of them it gives ("", None).
func (ks Keys) Pick(tags map[string]string) (owner, from string) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, want := range ks.order {
		for _, k := range keys {
			if norm(k) == want {
				if v := clean(tags[k]); v != "" {
					return ks.named(v), "tag " + clean(k)
				}
			}
		}
	}
	if v := clean(tags[stackKey]); v != "" {
		return ks.named(v), "CloudFormation stack"
	}
	if v := clean(tags[namespaceKey]); v != "" {
		return ks.named(v), "Kubernetes namespace"
	}
	// Last: the Kubernetes cluster that made it (its owner, not the user's
	// shared subnets: only "owned").
	for _, k := range keys {
		if c := strings.TrimPrefix(k, "kubernetes.io/cluster/"); c != k && tags[k] == "owned" && clean(c) != "" {
			return ks.named("Kubernetes cluster " + clean(c)), "Kubernetes cluster tag" // the key holds the name: never in the source label
		}
	}
	for _, k := range []string{"eks:eks-cluster-name", "KubernetesCluster"} {
		if v := clean(tags[k]); v != "" {
			return ks.named("Kubernetes cluster " + v), "tag " + k
		}
	}
	return "", None
}

// clean: a tag can hold anything; reports get it trimmed, printable and short.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 128 {
		s = string(r[:128]) + "..."
	}
	return s
}

// Ref names a resource as its ARN does after the account
// ("ec2:volume/vol-0abc"), the way findings do (Finding.TagRef). It evens
// out what differs between an ARN and a finding: a log group ARN may end in
// ":*", SageMaker ARNs are lower case, and a Secrets Manager ARN adds six
// random characters to the secret's name.
func Ref(arn string) string {
	p := strings.SplitN(arn, ":", 6)
	if len(p) != 6 || p[0] != "arn" || p[2] == "" || p[5] == "" {
		return ""
	}
	ref := normRef(p[2] + ":" + strings.TrimPrefix(p[5], "/")) // API Gateway: "/restapis/..."
	if strings.HasPrefix(ref, "secretsmanager:secret:") {
		ref = secretSuffix.ReplaceAllString(ref, "")
	}
	return ref
}

var secretSuffix = regexp.MustCompile(`-[A-Za-z0-9]{6}$`)

func normRef(ref string) string {
	ref = strings.TrimSuffix(ref, ":*")
	if strings.HasPrefix(ref, "sagemaker:") {
		ref = strings.ToLower(ref)
	}
	return ref
}

// Type is the tag:GetResources filter that finds a ref's resource: the
// service and resource type, as in the ARN ("ec2:volume", "logs:log-group"),
// or the service alone when its ARNs name no type (an S3 bucket: "s3").
func Type(ref string) string {
	svc, rest, ok := strings.Cut(ref, ":")
	if !ok || svc == "" || rest == "" {
		return ""
	}
	if i := strings.IndexAny(rest, "/:"); i > 0 {
		return svc + ":" + rest[:i]
	}
	if i := strings.IndexAny(rest, "/:"); i == 0 {
		return ""
	}
	return svc
}

// Tagger is the Resource Groups Tagging API call the lookup uses.
type Tagger interface {
	GetResources(context.Context, *rgt.GetResourcesInput, ...func(*rgt.Options)) (*rgt.GetResourcesOutput, error)
}

// Attach sets Owner and OwnerFrom on the findings about one resource (those
// with a TagRef). A finding whose tags couldn't be read keeps both empty, so
// it never counts as unowned, and the failed read is listed in r.Skipped.
func Attach(ctx context.Context, r *inventory.Region, fs []checks.Finding, api Tagger, ks Keys) {
	tags := ks.known(r)
	types := map[string]bool{}
	for _, f := range fs {
		ref := normRef(f.TagRef)
		if _, ok := tags[ref]; !ok && ref != "" {
			if t := Type(ref); t != "" {
				types[t] = true
			}
		}
	}
	order := make([]string, 0, len(types))
	for t := range types {
		order = append(order, t)
	}
	sort.Strings(order)
	read := map[string]bool{}
	for _, t := range order {
		if api == nil {
			break
		}
		err := ks.sweep(ctx, api, t, tags)
		if err == nil {
			read[t] = true
			continue
		}
		what := inventory.ShortError(err)
		if denied(err) {
			r.Skipped = append(r.Skipped, inventory.Skip{Region: r.Name, What: "tag:GetResources", Error: what})
			break // every other type would be denied too
		}
		r.Skipped = append(r.Skipped, inventory.Skip{Region: r.Name, What: "tag:GetResources", Error: what + " (" + t + ")"})
	}
	for i := range fs {
		ref := normRef(fs[i].TagRef)
		if ref == "" {
			continue
		}
		t, ok := tags[ref]
		if !ok && !read[Type(ref)] {
			continue // couldn't look
		}
		fs[i].Owner, fs[i].OwnerFrom = ks.Pick(t) // not in a full read: untagged
	}
}

func denied(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		c := api.ErrorCode()
		return strings.Contains(c, "AccessDenied") || strings.Contains(c, "Unauthorized")
	}
	return false
}

// known: the owner tags of resources whose describe calls already returned
// all their tags, so they need no tag:GetResources. A resource listed here
// with no owner tags really has none.
func (ks Keys) known(r *inventory.Region) map[string]map[string]string {
	m := map[string]map[string]string{}
	ec2 := func(ref string, tags []ec2types.Tag) {
		t := map[string]string{}
		for _, x := range tags {
			if k := aws.ToString(x.Key); ks.relevant(k) {
				t[k] = aws.ToString(x.Value)
			}
		}
		m[ref] = t
	}
	for _, v := range r.Volumes {
		ec2("ec2:volume/"+aws.ToString(v.VolumeId), v.Tags)
	}
	// REST API stages come with their tags (GetStages): no tag:GetResources sweep.
	for _, s := range r.RestStages {
		t := map[string]string{}
		for k, v := range s.Tags {
			if ks.relevant(k) {
				t[k] = v
			}
		}
		m[s.TagRef()] = t
	}
	// Accelerators come with their tags (ListTagsForResource) when that read worked.
	for _, a := range r.Accelerators {
		if a.Tags != nil {
			t := map[string]string{}
			for k, v := range a.Tags {
				if ks.relevant(k) {
					t[k] = v
				}
			}
			m[a.TagRef()] = t
		}
	}
	for _, in := range r.Instances {
		ec2("ec2:instance/"+aws.ToString(in.InstanceId), in.Tags)
	}
	for _, a := range r.Addresses {
		if id := aws.ToString(a.AllocationId); id != "" {
			ec2("ec2:elastic-ip/"+id, a.Tags)
		}
	}
	for _, s := range r.Snapshots {
		ec2("ec2:snapshot/"+aws.ToString(s.SnapshotId), s.Tags)
	}
	for _, im := range r.Images {
		ec2("ec2:image/"+aws.ToString(im.ImageId), im.Tags)
	}
	for _, n := range r.NatGateways {
		ec2("ec2:natgateway/"+aws.ToString(n.NatGatewayId), n.Tags)
	}
	for _, e := range r.VpcEndpoints {
		ec2("ec2:vpc-endpoint/"+aws.ToString(e.VpcEndpointId), e.Tags)
	}
	for _, s := range r.Secrets {
		t := map[string]string{}
		for _, x := range s.Tags {
			if k := aws.ToString(x.Key); ks.relevant(k) {
				t[k] = aws.ToString(x.Value)
			}
		}
		m["secretsmanager:secret:"+aws.ToString(s.Name)] = t
	}
	return m
}

// sweep reads the owner tags of every tagged resource of one type. A type
// read in full tells "untagged" apart from "unknown": GetResources doesn't
// return resources that have no tags.
func (ks Keys) sweep(ctx context.Context, api Tagger, typ string, into map[string]map[string]string) error {
	p := rgt.NewGetResourcesPaginator(api, &rgt.GetResourcesInput{ResourceTypeFilters: []string{typ}, ResourcesPerPage: aws.Int32(100)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, m := range page.ResourceTagMappingList {
			ref := Ref(aws.ToString(m.ResourceARN))
			if ref == "" {
				continue
			}
			t := map[string]string{}
			for _, x := range m.Tags {
				if k := aws.ToString(x.Key); ks.relevant(k) {
					t[k] = aws.ToString(x.Value)
				}
			}
			into[ref] = t
		}
	}
	return nil
}
