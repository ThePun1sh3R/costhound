// Package runlog records every AWS API call a costhound run makes, and
// refuses, before anything is sent, any call that could change something or
// that returns secret values or data. The run's ID goes into the user agent,
// so the account's own CloudTrail can confirm what the run did.
package runlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
)

// Call is one kind of call the run made, with how often and how it ended:
// ok, denied (no permission), throttled, error, or refused (by costhound,
// never sent).
type Call struct {
	Service   string `json:"service"`
	Operation string `json:"operation"`
	Region    string `json:"region"`
	Outcome   string `json:"outcome"`
	Count     int    `json:"count"`
}

// Log is one run's record.
type Log struct {
	ID      string
	mu      sync.Mutex
	n       map[Call]int    // Count unset in the key
	allowed map[string]bool // opt-in reads this run's flags allow
}

// New starts a run record with a random ID.
func New() *Log {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		b = []byte("norand")
	}
	return &Log{ID: hex.EncodeToString(b), n: map[Call]int{}}
}

// UserAgent is the part of every request's user agent that names this run.
func (l *Log) UserAgent() string { return "costhound-run/" + l.ID }

// Attach makes every client built from cfg (or a copy of it) record its
// calls, refuse the ones costhound must never make, and name the run in its
// user agent.
func (l *Log) Attach(cfg *aws.Config) {
	cfg.APIOptions = append(cfg.APIOptions,
		awsmiddleware.AddUserAgentKeyValue("costhound-run", l.ID),
		func(st *middleware.Stack) error {
			// After: the service and operation names are set by then.
			return st.Initialize.Add(middleware.InitializeMiddlewareFunc("CosthoundRunLog", l.handle), middleware.After)
		})
}

func (l *Log) handle(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
	c := Call{Service: awsmiddleware.GetServiceID(ctx), Operation: awsmiddleware.GetOperationName(ctx), Region: awsmiddleware.GetRegion(ctx)}
	if why := l.refused(c.Operation); why != "" {
		c.Outcome = "refused"
		l.add(c)
		return middleware.InitializeOutput{}, middleware.Metadata{}, fmt.Errorf("costhound refused to call %s %s: %s", c.Service, c.Operation, why)
	}
	out, md, err := next.HandleInitialize(ctx, in)
	c.Outcome = outcome(err)
	l.add(c)
	return out, md, err
}

func (l *Log) add(c Call) {
	l.mu.Lock()
	l.n[c]++
	l.mu.Unlock()
}

// Calls lists what the run called, by service, operation, region and outcome.
func (l *Log) Calls() []Call {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Call, 0, len(l.n))
	for c, n := range l.n {
		c.Count = n
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.Outcome < b.Outcome
	})
	return out
}

// readPrefixes are the operations that only read. Anything else is refused,
// except AssumeRole, which an Organization scan needs to read each member
// account (it changes nothing there).
var readPrefixes = []string{"Describe", "List", "Get", "Head", "BatchGet", "Lookup", "Search"}

// neverCalled are reads that return secret values, user data or customer
// data: costhound never needs them, so it refuses them even if a permission
// would allow it.
var neverCalled = map[string]string{
	"GetSecretValue":                            "secret values",
	"BatchGetSecretValue":                       "secret values",
	"GetParameter":                              "parameter values",
	"GetParameters":                             "parameter values",
	"GetParametersByPath":                       "parameter values",
	"GetParameterHistory":                       "parameter values",
	"GetObject":                                 "object data",
	"GetObjectTorrent":                          "object data",
	"GetLogEvents":                              "log contents",
	"GetQueryResults":                           "log contents",
	"GetLogRecord":                              "log contents",
	"GetPasswordData":                           "an instance's password",
	"GetConsoleOutput":                          "an instance's console output",
	"GetConsoleScreenshot":                      "an instance's screen",
	"DescribeInstanceAttribute":                 "user data",
	"GetLaunchTemplateData":                     "user data",
	"DescribeLaunchTemplateVersions":            "user data",
	"DescribeVpnConnections":                    "VPN tunnel keys",
	"GetVpnConnectionDeviceSampleConfiguration": "VPN tunnel keys",
	"GetFunction":                               "function code",
}

// optIn are reads costhound makes only when a flag asks for them.
var optIn = map[string]string{"LookupEvents": "audit-log records: pass -creators to allow it"}

// Allow lets one opt-in read through for this run.
func (l *Log) Allow(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.allowed == nil {
		l.allowed = map[string]bool{}
	}
	l.allowed[op] = true
}

func (l *Log) refused(op string) string {
	if why, ok := optIn[op]; ok {
		l.mu.Lock()
		allowed := l.allowed[op]
		l.mu.Unlock()
		if !allowed {
			return "it returns " + why
		}
	}
	return Refused(op)
}

// Refused says why costhound won't make a call ("" when it will).
func Refused(op string) string {
	if why, ok := neverCalled[op]; ok {
		return "it returns " + why
	}
	if op == "AssumeRole" {
		return ""
	}
	for _, p := range readPrefixes {
		if strings.HasPrefix(op, p) {
			return ""
		}
	}
	return "not a read-only call"
}

func outcome(err error) string {
	if err == nil {
		return "ok"
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		c := api.ErrorCode()
		switch {
		case strings.Contains(c, "AccessDenied"), strings.Contains(c, "Unauthorized"), c == "AuthFailure", c == "OptInRequired":
			return "denied"
		case strings.Contains(c, "Throttl"), c == "RequestLimitExceeded", c == "TooManyRequestsException", c == "SlowDown":
			return "throttled"
		}
	}
	return "error"
}
