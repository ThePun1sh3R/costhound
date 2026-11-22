package runlog

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// fakeHTTP answers every request with one canned response and remembers what was sent.
type fakeHTTP struct {
	status int
	body   string
	seen   []*http.Request
}

func (f *fakeHTTP) Do(r *http.Request) (*http.Response, error) {
	f.seen = append(f.seen, r)
	return &http.Response{StatusCode: f.status, Header: http.Header{"Content-Type": {"text/xml"}},
		Body: io.NopCloser(strings.NewReader(f.body)), Request: r}, nil
}

func client(l *Log, h *fakeHTTP) *ec2.Client {
	cfg := aws.Config{Region: "eu-west-1", HTTPClient: h, RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")}
	l.Attach(&cfg)
	return ec2.NewFromConfig(cfg)
}

func TestReadsAreRecordedAndNamed(t *testing.T) {
	l := New()
	h := &fakeHTTP{status: 200, body: `<DescribeVolumesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><volumeSet/></DescribeVolumesResponse>`}
	c := client(l, h)
	for i := 0; i < 2; i++ {
		if _, err := c.DescribeVolumes(context.Background(), &ec2.DescribeVolumesInput{}); err != nil {
			t.Fatal(err)
		}
	}
	got := l.Calls()
	if len(got) != 1 || got[0] != (Call{Service: "EC2", Operation: "DescribeVolumes", Region: "eu-west-1", Outcome: "ok", Count: 2}) {
		t.Errorf("calls = %+v", got)
	}
	if ua := h.seen[0].Header.Get("User-Agent"); !strings.Contains(ua, l.UserAgent()) {
		t.Errorf("the run must be named in the user agent (CloudTrail shows it): %q", ua)
	}
}

func TestWritesAndSecretReadsAreNeverSent(t *testing.T) {
	l := New()
	h := &fakeHTTP{status: 200, body: `<x/>`}
	c := client(l, h)
	if _, err := c.DeleteVolume(context.Background(), &ec2.DeleteVolumeInput{VolumeId: aws.String("vol-1")}); err == nil || !strings.Contains(err.Error(), "not a read-only call") {
		t.Errorf("DeleteVolume: %v", err)
	}
	if _, err := c.GetPasswordData(context.Background(), &ec2.GetPasswordDataInput{InstanceId: aws.String("i-1")}); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("GetPasswordData: %v", err)
	}
	if _, err := c.DescribeVpnConnections(context.Background(), &ec2.DescribeVpnConnectionsInput{}); err == nil {
		t.Error("DescribeVpnConnections returns tunnel keys: it must be refused")
	}
	if len(h.seen) != 0 {
		t.Errorf("refused calls must never reach AWS: %d sent", len(h.seen))
	}
	for _, c := range l.Calls() {
		if c.Outcome != "refused" {
			t.Errorf("%+v", c)
		}
	}
}

func TestDeniedCallsAreCounted(t *testing.T) {
	l := New()
	h := &fakeHTTP{status: 403, body: `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>no</Message></Error></Errors><RequestID>1</RequestID></Response>`}
	if _, err := client(l, h).DescribeHosts(context.Background(), &ec2.DescribeHostsInput{}); err == nil {
		t.Fatal("want the 403")
	}
	if got := l.Calls(); len(got) != 1 || got[0].Outcome != "denied" {
		t.Errorf("calls = %+v", got)
	}
}

func TestRefused(t *testing.T) {
	for op, ok := range map[string]bool{
		"DescribeVolumes": true, "ListMetrics": true, "GetMetricData": true, "GetResources": true, "HeadBucket": true, "AssumeRole": true,
		"GetCallerIdentity": true, "BatchGetImage": true,
		"DeleteVolume": false, "CreateSnapshot": false, "PutBucketLifecycleConfiguration": false, "ModifyVolume": false,
		"StartInstances": false, "Decrypt": false, "InvokeModel": false, "GetSecretValue": false, "GetObject": false, "GetLogEvents": false,
		"GetParametersByPath": false, "DescribeLaunchTemplateVersions": false, "GetFunction": false, "": false,
	} {
		if got := Refused(op) == ""; got != ok {
			t.Errorf("Refused(%q) = %q", op, Refused(op))
		}
	}
}

// CloudTrail's event history is an audit log: LookupEvents is refused unless
// -creators allows it for the run.
func TestLookupEventsIsOptIn(t *testing.T) {
	l := New()
	if l.refused("LookupEvents") == "" {
		t.Error("LookupEvents without -creators must be refused")
	}
	l.Allow("LookupEvents")
	if l.refused("LookupEvents") != "" || l.refused("DescribeVolumes") != "" || l.refused("GetSecretValue") == "" {
		t.Error("Allow lets LookupEvents through, and nothing else changes")
	}
}
