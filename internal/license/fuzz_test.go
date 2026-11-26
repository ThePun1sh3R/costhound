package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// FuzzVerify feeds arbitrary license key bytes through Verify. Rejected keys
// must not yield account/org/project credentials (except a signed-but-expired
// key, which still authenticates the payload).
func FuzzVerify(f *testing.F) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	goodPayload, _ := json.Marshal(Claims{ID: "abc123", Plan: PlanPro, Accounts: []string{"123456789012"}, Issued: now.Unix()})
	good := Prefix + b64.EncodeToString(goodPayload)
	good = good + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(good)))

	f.Add([]byte(""))
	f.Add([]byte("not-a-key"))
	f.Add([]byte(Prefix))
	f.Add([]byte(good))
	f.Add([]byte("  " + good + "\n"))
	if b, err := os.ReadFile("testdata/worker-key.txt"); err == nil {
		f.Add([]byte(strings.TrimSpace(string(b))))
	}
	// Malformed / truncated shapes from the rejection table.
	payload, sig, _ := strings.Cut(strings.TrimPrefix(good, Prefix), ".")
	f.Add([]byte(Prefix + payload))
	f.Add([]byte(Prefix + payload + "."))
	f.Add([]byte(Prefix + payload + ".!!!"))
	f.Add([]byte(Prefix + payload + "." + sig[:20]))
	f.Add([]byte(good + ".zz"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16<<10 {
			return
		}
		c, err := Verify(string(data), pub, now)
		if err == nil {
			if c.ID == "" || Rank(c.Plan) < 1 {
				t.Fatalf("accepted key must have id and known plan: %+v", c)
			}
			return
		}
		if errors.Is(err, ErrExpired) {
			// Authenticated claims are returned with expiry; still not a usable key.
			return
		}
		if len(c.Accounts) != 0 || c.Org != "" || len(c.Projects) != 0 || len(c.Subscriptions) != 0 || c.Tenant != "" {
			t.Fatalf("rejected key must not yield credentials: %+v (%v)", c, err)
		}
	})
}
