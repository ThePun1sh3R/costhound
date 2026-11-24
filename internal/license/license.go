// Package license checks costhound Pro license keys, offline.
//
// A key is "CH1." + base64url(payload JSON) + "." + base64url(ed25519 signature).
// The signature covers the exact bytes "CH1." + base64url(payload), so the
// payload can't be edited without breaking it. The binary carries only the
// public key; the private key stays with the seller and never ships.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Prefix starts every license key.
const Prefix = "CH1."

// The plans, cheapest first. PlanFree is what runs without a valid key.
// Each plan includes everything in the plans before it.
const (
	PlanFree = "free"
	PlanPro  = "pro"
	PlanTeam = "team"
)

// Rank orders plans: free 0, pro 1, team 2; unknown plans -1.
func Rank(plan string) int {
	switch plan {
	case PlanFree:
		return 0
	case PlanPro:
		return 1
	case PlanTeam:
		return 2
	}
	return -1
}

// EnvVar holds a license key (or a path to a file with one).
const EnvVar = "COSTHOUND_LICENSE"

// Claims is what a license key says.
//
// A key only works for the AWS accounts it names, or for every account in
// the AWS Organization it names. costhound reads the account it scans from
// STS, so a key passed to someone else does nothing on their AWS.
//
// Optional Azure fields (absent on older keys): Subscriptions lists Azure
// subscription GUIDs; Tenant names a whole Microsoft Entra tenant (Team).
type Claims struct {
	ID            string   `json:"id"`
	Plan          string   `json:"plan"`
	Accounts      []string `json:"accounts,omitempty"`      // 12-digit AWS account IDs
	Org           string   `json:"org,omitempty"`           // AWS Organization ID (o-...)
	Subscriptions []string `json:"subscriptions,omitempty"` // Azure subscription GUIDs (lowercase)
	Tenant        string   `json:"tenant,omitempty"`        // Azure tenant GUID (lowercase)
	Issued        int64    `json:"iat"`
	Expires       int64    `json:"exp,omitempty"` // unix seconds; 0 = never
	Note          string   `json:"note,omitempty"`
}

// Covers reports whether the key is valid for this AWS account. org is the
// account's Organization ID, or "" when unknown or not in one.
func (c Claims) Covers(account, org string) bool {
	if c.Org != "" && org == c.Org {
		return true
	}
	for _, a := range c.Accounts {
		if a == account {
			return true
		}
	}
	return false
}

// Scope describes what the key covers, for messages.
func (c Claims) Scope() string {
	var parts []string
	if c.Org != "" {
		parts = append(parts, "organization "+c.Org)
	}
	if len(c.Accounts) > 0 {
		parts = append(parts, "account(s) "+strings.Join(c.Accounts, ", "))
	}
	if c.Tenant != "" {
		parts = append(parts, "tenant "+c.Tenant)
	}
	if len(c.Subscriptions) > 0 {
		parts = append(parts, "subscription(s) "+strings.Join(c.Subscriptions, ", "))
	}
	return strings.Join(parts, " and ")
}

// RenewURL is where to buy or renew a key.
const RenewURL = "https://costhound.dev"

// WarnWithin is how soon before Expires a run prints ExpiryWarning.
const WarnWithin = 7 * 24 * time.Hour

var (
	ErrMalformed = errors.New("this isn't a costhound license key (it should start with " + Prefix + ")")
	ErrSignature = errors.New("the license key doesn't check out (a typo, or not issued for costhound)")
	ErrExpired   = errors.New("the license key has expired")
	ErrPlan      = errors.New("the license key is for a plan this version doesn't know; update costhound")
)

// Strict: one spelling per key (no variants that differ only in padding bits).
var b64 = base64.RawURLEncoding.Strict()

// Verify checks a license key against pub and returns what it says.
// Whitespace anywhere in the key is ignored, so a key wrapped by an email
// client or pasted with a trailing newline still works.
func Verify(key string, pub ed25519.PublicKey, now time.Time) (Claims, error) {
	key = strings.Join(strings.Fields(key), "")
	if !strings.HasPrefix(key, Prefix) {
		return Claims{}, ErrMalformed
	}
	payloadB64, sigB64, ok := strings.Cut(strings.TrimPrefix(key, Prefix), ".")
	if !ok || payloadB64 == "" || sigB64 == "" || strings.Contains(sigB64, ".") {
		return Claims{}, ErrMalformed
	}
	sig, err := b64.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Claims{}, ErrMalformed
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(Prefix+payloadB64), sig) {
		return Claims{}, ErrSignature
	}
	payload, err := b64.DecodeString(payloadB64)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil || c.ID == "" {
		return Claims{}, ErrMalformed
	}
	if Rank(c.Plan) < 1 {
		return Claims{}, ErrPlan
	}
	if c.Expires != 0 && now.Unix() >= c.Expires {
		end := time.Unix(c.Expires, 0).UTC().Format("2006-01-02")
		return c, fmt.Errorf("%w: it ended on %s; renew at %s", ErrExpired, end, RenewURL)
	}
	return c, nil
}

// ExpiryWarning is the one stderr line when a paid key ends within WarnWithin,
// or "" when there is nothing to say (no end, more than WarnWithin left, or
// already expired). Callers print it before a scan; it never goes to stdout.
func ExpiryWarning(c Claims, now time.Time) string {
	if c.Expires == 0 {
		return ""
	}
	end := time.Unix(c.Expires, 0).UTC()
	left := end.Sub(now)
	if left <= 0 || left > WarnWithin {
		return ""
	}
	days := int((left + 24*time.Hour - 1) / (24 * time.Hour)) // rounded up: the last day says 1, not 0
	plan := c.Plan
	if plan != "" {
		plan = strings.ToUpper(plan[:1]) + plan[1:]
	}
	return fmt.Sprintf("costhound: your %s key ends on %s (in %d day(s)): renew at %s",
		plan, end.Format("2006-01-02"), days, RenewURL)
}

// Find returns the license key to use and where it came from, looking at, in
// order: the -license flag, the COSTHOUND_LICENSE variable, and the file
// <user config dir>/costhound/license. The flag and the variable may hold the
// key itself or a path to a file containing it. No key anywhere is not an
// error: it returns "", "".
func Find(flagValue string) (key, from string, err error) {
	if flagValue != "" {
		return keyOrFile(flagValue, "-license")
	}
	if v := os.Getenv(EnvVar); v != "" {
		return keyOrFile(v, EnvVar)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", nil
	}
	path := filepath.Join(dir, "costhound", "license")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", path, err
	}
	return strings.TrimSpace(string(b)), path, nil
}

func keyOrFile(v, from string) (string, string, error) {
	if strings.HasPrefix(strings.TrimSpace(v), Prefix) {
		return v, from, nil
	}
	b, err := os.ReadFile(v)
	if err != nil {
		return "", v, fmt.Errorf("%s is neither a license key nor a readable file: %w", from, err)
	}
	return strings.TrimSpace(string(b)), v, nil
}

// Status is the plan a run ends up on, and why.
type Status struct {
	Plan    string // PlanFree, PlanPro or PlanTeam
	ID      string // license ID when paid
	From    string // where the key came from
	Problem string // why a key that was found wasn't accepted
	Claims  Claims // the key's contents when it verified
}

// ForAccount checks a verified key against the account being scanned and
// drops to the free plan, with a reason, if the key doesn't cover it.
func (s Status) ForAccount(account, org string) Status {
	if Rank(s.Plan) < 1 || s.Claims.Covers(account, org) {
		return s
	}
	return Status{Plan: PlanFree, From: s.From, Problem: fmt.Sprintf(
		"the license (%s) is for %s, not AWS account %s", s.ID, s.Claims.Scope(), account)}
}

// Resolve finds and checks the license. A missing key gives the free plan
// quietly; a bad key gives the free plan plus a Problem to show the user.
func Resolve(flagValue string, pub ed25519.PublicKey, now time.Time) Status {
	key, from, err := Find(flagValue)
	if err != nil {
		return Status{Plan: PlanFree, From: from, Problem: err.Error()}
	}
	if key == "" {
		return Status{Plan: PlanFree}
	}
	c, err := Verify(key, pub, now)
	if err != nil {
		return Status{Plan: PlanFree, From: from, Problem: err.Error()}
	}
	return Status{Plan: c.Plan, ID: c.ID, From: from, Claims: c}
}
