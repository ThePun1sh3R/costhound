package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func newKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const acct = "123456789012"

func issue(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()
	if len(c.Accounts) == 0 && c.Org == "" {
		c.Accounts = []string{acct}
	}
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	signed := Prefix + b64.EncodeToString(payload) // the seller's format (Sign, in license_pro.go)
	return signed + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(signed)))
}

func TestEmbeddedPublicKey(t *testing.T) {
	if len(PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("embedded public key is broken (len %d): every license would be rejected", len(PublicKey))
	}
}

func TestRoundTrip(t *testing.T) {
	pub, priv := newKeys(t)
	key := issue(t, priv, Claims{ID: "abc123", Plan: PlanPro, Issued: now.Unix(), Note: "tx 0xdead"})
	if !strings.HasPrefix(key, Prefix) {
		t.Fatalf("key %q lacks prefix", key)
	}
	c, err := Verify(key, pub, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "abc123" || c.Plan != PlanPro || c.Note != "tx 0xdead" {
		t.Fatalf("claims = %+v", c)
	}
}

func TestWhitespaceIgnored(t *testing.T) {
	pub, priv := newKeys(t)
	key := issue(t, priv, Claims{ID: "x", Plan: PlanPro})
	wrapped := "  " + key[:20] + "\n" + key[20:40] + "\r\n\t" + key[40:] + "\n"
	if _, err := Verify(wrapped, pub, now); err != nil {
		t.Fatalf("wrapped key rejected: %v", err)
	}
}

func TestRejections(t *testing.T) {
	pub, priv := newKeys(t)
	otherPub, otherPriv := newKeys(t)
	_ = otherPub
	good := issue(t, priv, Claims{ID: "x", Plan: PlanPro})
	payload, sig, _ := strings.Cut(strings.TrimPrefix(good, Prefix), ".")

	// A payload signed by someone else, spliced onto a valid signature.
	forged := issue(t, otherPriv, Claims{ID: "someone-else", Plan: PlanPro})
	forgedPayload, _, _ := strings.Cut(strings.TrimPrefix(forged, Prefix), ".")

	cases := map[string]struct {
		key  string
		pub  ed25519.PublicKey
		want error
	}{
		"empty":                 {"", pub, ErrMalformed},
		"no prefix":             {strings.TrimPrefix(good, Prefix), pub, ErrMalformed},
		"no signature":          {Prefix + payload, pub, ErrMalformed},
		"empty signature":       {Prefix + payload + ".", pub, ErrMalformed},
		"extra part":            {good + ".zz", pub, ErrMalformed},
		"signature not base64":  {Prefix + payload + ".!!!", pub, ErrMalformed},
		"short signature":       {Prefix + payload + "." + sig[:20], pub, ErrMalformed},
		"wrong public key":      {good, otherPub, ErrSignature},
		"nil public key":        {good, nil, ErrSignature},
		"payload swapped":       {Prefix + forgedPayload + "." + sig, pub, ErrSignature},
		"padding bits set":      {Prefix + payload + "." + paddingVariant(sig), pub, ErrMalformed},
		"one char of sig off":   {Prefix + payload + "." + flip(sig), pub, ErrSignature},
		"one char of body off":  {Prefix + flip(payload) + "." + sig, pub, ErrSignature},
		"signed non-JSON":       {signRaw(priv, "bm90IGpzb24"), pub, ErrMalformed},
		"signed JSON, no id":    {issueRaw(priv, `{"plan":"pro"}`), pub, ErrMalformed},
		"unknown plan":          {issueRaw(priv, `{"id":"x","plan":"enterprise","accounts":["123456789012"]}`), pub, ErrPlan},
		"free plan isn't a key": {issueRaw(priv, `{"id":"x","plan":"free","accounts":["123456789012"]}`), pub, ErrPlan},
		"expired":               {issue(t, priv, Claims{ID: "x", Plan: PlanPro, Expires: now.Unix()}), pub, ErrExpired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(tc.key, tc.pub, now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if errors.Is(err, ErrExpired) {
				end := time.Unix(now.Unix(), 0).UTC().Format("2006-01-02")
				if !strings.Contains(err.Error(), end) || !strings.Contains(err.Error(), RenewURL) {
					t.Fatalf("expired error must name the end date and renew link: %v", err)
				}
			}
		})
	}
}

func TestExpiryWarning(t *testing.T) {
	end3 := now.Add(3 * 24 * time.Hour)
	want3 := fmt.Sprintf("costhound: your Pro key ends on %s (in 3 day(s)): renew at %s",
		end3.UTC().Format("2006-01-02"), RenewURL)
	if got := ExpiryWarning(Claims{Plan: PlanPro, Expires: end3.Unix()}, now); got != want3 {
		t.Fatalf("3 days left:\n got %q\nwant %q", got, want3)
	}
	if got := ExpiryWarning(Claims{Plan: PlanPro, Expires: now.Add(8 * 24 * time.Hour).Unix()}, now); got != "" {
		t.Fatalf("8 days left: got %q, want empty", got)
	}
	if got := ExpiryWarning(Claims{Plan: PlanPro, Expires: 0}, now); got != "" {
		t.Fatalf("Expires 0: got %q, want empty", got)
	}
	pub, priv := newKeys(t)
	expired := issue(t, priv, Claims{ID: "x", Plan: PlanPro, Expires: now.Unix()})
	c, err := Verify(expired, pub, now)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	end := time.Unix(now.Unix(), 0).UTC().Format("2006-01-02")
	if !strings.Contains(err.Error(), end) || !strings.Contains(err.Error(), RenewURL) {
		t.Fatalf("expired error must name the end date and renew link: %v", err)
	}
	if got := ExpiryWarning(c, now); got != "" {
		t.Fatalf("expired Claims: got warning %q, want empty", got)
	}
}

func TestNotYetExpired(t *testing.T) {
	pub, priv := newKeys(t)
	key := issue(t, priv, Claims{ID: "x", Plan: PlanPro, Expires: now.Add(time.Second).Unix()})
	if _, err := Verify(key, pub, now); err != nil {
		t.Fatal(err)
	}
}

func TestFindOrder(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg) // os.UserConfigDir on Linux
	t.Setenv("HOME", cfg)            // and on macOS
	t.Setenv(EnvVar, "")

	if k, from, err := Find(""); err != nil || k != "" || from != "" {
		t.Fatalf("nothing set: got %q %q %v", k, from, err)
	}

	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, "costhound", "license")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(Prefix+"fromfile.sig\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, from, _ := Find(""); k != Prefix+"fromfile.sig" || from != path {
		t.Fatalf("config file: got %q from %q", k, from)
	}

	t.Setenv(EnvVar, Prefix+"fromenv.sig")
	if k, from, _ := Find(""); k != Prefix+"fromenv.sig" || from != EnvVar {
		t.Fatalf("env beats file: got %q from %q", k, from)
	}

	if k, from, _ := Find(Prefix + "fromflag.sig"); k != Prefix+"fromflag.sig" || from != "-license" {
		t.Fatalf("flag beats env: got %q from %q", k, from)
	}

	other := filepath.Join(cfg, "my.key")
	if err := os.WriteFile(other, []byte("  "+Prefix+"inpath.sig\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, from, _ := Find(other); k != Prefix+"inpath.sig" || from != other {
		t.Fatalf("flag as a path: got %q from %q", k, from)
	}

	if _, _, err := Find(filepath.Join(cfg, "missing")); err == nil {
		t.Fatal("a flag that is neither a key nor a file should be an error")
	}
}

func TestResolve(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv(EnvVar, "")
	pub, priv := newKeys(t)

	if s := Resolve("", pub, now); s.Plan != PlanFree || s.Problem != "" {
		t.Fatalf("no key: %+v", s)
	}
	good := issue(t, priv, Claims{ID: "lic1", Plan: PlanPro})
	if s := Resolve(good, pub, now); s.Plan != PlanPro || s.ID != "lic1" || s.Problem != "" {
		t.Fatalf("good key: %+v", s)
	}
	if s := Resolve(flip(good), pub, now); s.Plan != PlanFree || s.Problem == "" {
		t.Fatalf("bad key must fall back to free with a reason: %+v", s)
	}
	if s := Resolve(filepath.Join(cfg, "missing"), pub, now); s.Plan != PlanFree || s.Problem == "" {
		t.Fatalf("unreadable path must fall back to free with a reason: %+v", s)
	}
}

// flip changes one character in the middle, keeping it valid base64url.
func flip(s string) string {
	i := len(s) / 2
	repl := byte('A')
	if s[i] == 'A' {
		repl = 'B'
	}
	return s[:i] + string(repl) + s[i+1:]
}

func signRaw(priv ed25519.PrivateKey, payloadB64 string) string {
	signed := Prefix + payloadB64
	return signed + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(signed)))
}

func issueRaw(priv ed25519.PrivateKey, json string) string {
	return signRaw(priv, b64.EncodeToString([]byte(json)))
}

// paddingVariant spells the same signature bytes differently by setting the
// unused low bits of the last character (64 bytes = 86 chars, 4 spare bits).
func paddingVariant(sig string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, sig[len(sig)-1])
	return sig[:len(sig)-1] + string(alphabet[i|0b11])
}

func TestPlans(t *testing.T) {
	if !(Rank(PlanFree) < Rank(PlanPro) && Rank(PlanPro) < Rank(PlanTeam)) || Rank("gold") != -1 {
		t.Fatal("plan ranks out of order")
	}
	pub, priv := newKeys(t)
	c, err := Verify(issue(t, priv, Claims{ID: "t", Plan: PlanTeam}), pub, now)
	if err != nil || c.Plan != PlanTeam {
		t.Fatalf("team key: %+v %v", c, err)
	}
}

func TestAccountLock(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv(EnvVar, "")
	pub, priv := newKeys(t)

	byAccount := issue(t, priv, Claims{ID: "a", Plan: PlanPro, Accounts: []string{"111111111111", "222222222222"}})
	byOrg := issue(t, priv, Claims{ID: "o", Plan: PlanTeam, Org: "o-abc123"})

	cases := []struct {
		name, key, account, org, plan string
	}{
		{"listed account", byAccount, "222222222222", "", PlanPro},
		{"other account", byAccount, "333333333333", "", PlanFree},
		{"other account, same org doesn't matter for an account key", byAccount, "333333333333", "o-abc123", PlanFree},
		{"org member", byOrg, "999999999999", "o-abc123", PlanTeam},
		{"other org", byOrg, "999999999999", "o-zzz999", PlanFree},
		{"org unknown", byOrg, "999999999999", "", PlanFree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Resolve(tc.key, pub, now).ForAccount(tc.account, tc.org)
			if s.Plan != tc.plan {
				t.Fatalf("plan = %s, want %s (%+v)", s.Plan, tc.plan, s)
			}
			if tc.plan == PlanFree && !strings.Contains(s.Problem, tc.account) {
				t.Fatalf("a refused key must say which account it was used on: %q", s.Problem)
			}
		})
	}
	// Free stays free, with no message.
	if s := Resolve("", pub, now).ForAccount("1", ""); s.Plan != PlanFree || s.Problem != "" {
		t.Fatalf("no key: %+v", s)
	}
}

func TestExpiryWarningLastDaySaysOne(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	got := ExpiryWarning(Claims{Plan: "pro", Expires: now.Add(2 * time.Hour).Unix()}, now)
	if !strings.Contains(got, "(in 1 day(s))") {
		t.Errorf("2 hours left should read 1 day, got %q", got)
	}
}
