package license

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"testing"
	"time"
)

// testdata/worker-key.txt is signed by the checkout Worker's JavaScript
// (costhound-private/checkout, test seed 32 x 0x07). The Go verifier must
// accept it byte for byte, or keys sold online wouldn't work in the binary.
func TestKeySignedByTheCheckoutWorker(t *testing.T) {
	b, err := os.ReadFile("testdata/worker-key.txt")
	if err != nil {
		t.Fatal(err)
	}
	pub := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)).Public().(ed25519.PublicKey)
	c, err := Verify(string(b), pub, time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("the Worker's key doesn't verify in Go: %v", err)
	}
	if c.ID != "abc" || c.Plan != PlanTeam || len(c.Accounts) != 1 || c.Accounts[0] != "123456789012" || c.Expires != 1826536000 || c.Note != "Base 0xab" {
		t.Fatalf("claims = %+v", c)
	}
	if !c.Covers("123456789012", "") || c.Covers("111111111111", "") {
		t.Fatal("account lock must hold for Worker-made keys")
	}
	// The real embedded key must NOT accept a test-seed key.
	if _, err := Verify(string(b), PublicKey, time.Unix(1800000000, 0)); err == nil {
		t.Fatal("a key signed with the test seed must not verify against the real public key")
	}
}
