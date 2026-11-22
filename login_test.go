package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
)

// A first run's errors say what to do, not only what the SDK saw.
func TestFirstRunHelp(t *testing.T) {
	missing := fmt.Errorf("load: %w", config.SharedConfigProfileNotExistError{Profile: "nope"})
	if s := configHelp(missing); !strings.Contains(s, `no AWS profile named "nope"`) || !strings.Contains(s, "aws configure list-profiles") {
		t.Errorf("unknown profile: %s", s)
	}
	none := errors.New("operation error STS: GetCallerIdentity, get identity: get credentials: failed to refresh cached credentials, no EC2 IMDS role found, operation error ec2imds: GetMetadata, request canceled, context deadline exceeded")
	if s := loginHelp(none); !strings.HasPrefix(s, "costhound: no AWS credentials found") || !strings.Contains(s, "aws login") {
		t.Errorf("no credentials: %s", s)
	}
	exp := fmt.Errorf("operation error STS: GetCallerIdentity, %w", &smithy.GenericAPIError{Code: "ExpiredToken", Message: "The security token included in the request is expired"})
	if s := loginHelp(exp); !strings.HasPrefix(s, "costhound: your AWS login has expired") {
		t.Errorf("expired: %s", s)
	}
	other := &smithy.GenericAPIError{Code: "AccessDenied", Message: "nope"}
	if s := loginHelp(other); !strings.HasPrefix(s, "costhound: the AWS login check failed") {
		t.Errorf("other: %s", s)
	}
}
