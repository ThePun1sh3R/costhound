package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// configHelp says what to do when the AWS config can't load: on a first
// run, usually a -profile that doesn't exist.
func configHelp(err error) string {
	var missing config.SharedConfigProfileNotExistError
	if errors.As(err, &missing) {
		return fmt.Sprintf("costhound: there's no AWS profile named %q in ~/.aws/config or ~/.aws/credentials.\n"+
			"List yours with: aws configure list-profiles", missing.Profile)
	}
	return fmt.Sprintf("costhound: can't load AWS config: %v", err)
}

// loginHelp says what to do when the login check (sts:GetCallerIdentity)
// fails, before the SDK's own words: no credentials at all, an expired
// login, or anything else.
func loginHelp(err error) string {
	msg := err.Error()
	var api smithy.APIError
	expired := errors.As(err, &api) && strings.Contains(api.ErrorCode(), "ExpiredToken") ||
		strings.Contains(msg, "has expired") || strings.Contains(msg, "token is expired")
	switch {
	case strings.Contains(msg, "no EC2 IMDS role found") || strings.Contains(msg, "failed to retrieve credentials") && !expired:
		return "costhound: no AWS credentials found (no AWS_ variables, no profile, not on an AWS machine).\n" +
			"Log in first, then run costhound again: aws configure, aws sso login or aws login; or pass -profile <name>.\n" +
			"(" + inventory.ShortError(err) + ")"
	case expired:
		return "costhound: your AWS login has expired. Log in again (aws sso login or aws login), then run costhound again.\n" +
			"(" + inventory.ShortError(err) + ")"
	}
	return "costhound: the AWS login check failed: " + inventory.ShortError(err) + "\n" +
		"Log in first (aws configure, aws sso login or aws login), or pass -profile."
}
