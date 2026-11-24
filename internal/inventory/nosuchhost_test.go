package inventory

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestIsNoSuchHostAndShortError(t *testing.T) {
	dns := &net.DNSError{Err: "no such host", Name: "apprunner.eu-north-1.amazonaws.com", IsNotFound: true}
	wrapped := fmt.Errorf("operation error AppRunner: ListServices: %w", dns)
	if !IsNoSuchHost(dns) || !IsNoSuchHost(wrapped) {
		t.Fatalf("DNS NXDOMAIN not detected: %v / %v", dns, wrapped)
	}
	if ShortError(dns) != NoSuchHost || ShortError(wrapped) != NoSuchHost {
		t.Fatalf("ShortError = %q / %q", ShortError(dns), ShortError(wrapped))
	}
	if IsNoSuchHost(errors.New("AccessDenied")) || ShortError(errors.New("context deadline exceeded")) == NoSuchHost {
		t.Fatal("non-DNS errors must not look like no such host")
	}
	timeout := &net.DNSError{Err: "i/o timeout", Name: "ec2.eu-north-1.amazonaws.com", IsTimeout: true}
	if IsNoSuchHost(timeout) {
		t.Fatal("DNS timeout is not NXDOMAIN")
	}
}

func TestServiceLabel(t *testing.T) {
	if got := ServiceLabel("apprunner:ListServices"); got != "App Runner" {
		t.Fatalf("got %q", got)
	}
	if got := ServiceLabel("glue:ListSessions"); got != "Glue" {
		t.Fatalf("got %q", got)
	}
	if got := ServiceLabel("s3:ListBuckets"); got != "s3" {
		t.Fatalf("unknown service: %q", got)
	}
}
