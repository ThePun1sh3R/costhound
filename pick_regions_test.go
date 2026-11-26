package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestPickRegionsFlag(t *testing.T) {
	got, err := pickRegions(context.Background(), aws.Config{}, "eu-west-1, us-east-1 ,")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "eu-west-1" || got[1] != "us-east-1" {
		t.Fatalf("flag list: %v", got)
	}
	// Duplicates in the flag are kept as given (caller asked for them twice).
	dup, err := pickRegions(context.Background(), aws.Config{}, "eu-west-1,eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dup) != 2 || dup[0] != "eu-west-1" || dup[1] != "eu-west-1" {
		t.Fatalf("duplicates: %v", dup)
	}
	// Unknown names are not validated here: they pass through for the scan to skip/deny.
	unk, err := pickRegions(context.Background(), aws.Config{}, "not-a-region-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if len(unk) != 1 || unk[0] != "not-a-region-xyz" {
		t.Fatalf("unknown: %v", unk)
	}
}

func TestPickRegionsDefaultDescribe(t *testing.T) {
	h := &regionsHTTP{body: `<DescribeRegionsResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <regionInfo>
    <item><regionName>eu-north-1</regionName></item>
    <item><regionName>eu-west-1</regionName></item>
  </regionInfo>
</DescribeRegionsResponse>`}
	cfg := aws.Config{
		Region:           "us-east-1",
		HTTPClient:       h,
		RetryMaxAttempts: 1,
		Credentials:      credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
	}
	got, err := pickRegions(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "eu-north-1" || got[1] != "eu-west-1" {
		t.Fatalf("defaults sorted: %v", got)
	}
	if h.calls == 0 {
		t.Fatal("DescribeRegions must be called when the flag is empty")
	}
}

type regionsHTTP struct {
	body  string
	calls int
}

func (h *regionsHTTP) Do(r *http.Request) (*http.Response, error) {
	h.calls++
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"text/xml"}},
		Body:       io.NopCloser(strings.NewReader(h.body)),
		Request:    r,
	}, nil
}
