//go:build !pro

package main

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/ThePun1sh3R/costhound/internal/report"
)

func attachProPreview(context.Context, aws.Config, string, settings, []string, *report.Report, bool) {
}
