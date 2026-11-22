package attest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeys(t *testing.T) {
	dir := t.TempDir()
	priv, pub := filepath.Join(dir, "k", "report-signing.key"), filepath.Join(dir, "k", "report-signing.pub")
	p, err := GenerateKey(priv, pub, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(priv); st.Mode().Perm() != 0o600 {
		t.Errorf("the private key must be 0600, is %v", st.Mode().Perm())
	}
	if _, err := GenerateKey(priv, pub, nil); err == nil {
		t.Error("an existing key must never be overwritten")
	}
	fromFile, err := ParsePublic(pub)
	if err != nil || !fromFile.Equal(p) {
		t.Fatalf("public key file: %v", err)
	}
	b, _ := os.ReadFile(pub)
	if inline, err := ParsePublic(string(b)); err != nil || !inline.Equal(p) {
		t.Fatalf("public key given inline: %v", err)
	}
	if _, err := ParsePublic("not a key"); err == nil {
		t.Error("garbage must not parse")
	}
}

// Verify (free: costhound verify) against a signature made by hand, the way
// the signer makes one: every claim in Message, an ed25519 signature over it.
func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	report := []byte(`{"tool":"costhound","total_monthly_usd":42}`)
	sum := sha256.Sum256(report)
	s := Signature{Format: "costhound-report-signature/1", ReportSHA256: hex.EncodeToString(sum[:]), Version: "v1.2.3", BinarySHA256: "abc123",
		RunID: "run1", SignedAt: time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC), PublicKey: base64.StdEncoding.EncodeToString(pub)}
	s.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, s.Message()))
	res, err := Verify(report, s, pub)
	if err != nil || !res.HashOK || !res.SigOK || !res.KeyMatches {
		t.Fatalf("a good signature must verify: %+v %v", res, err)
	}
	if res, _ := Verify([]byte(`{"tool":"costhound","total_monthly_usd":4}`), s, pub); res.HashOK {
		t.Error("a changed report must fail the hash")
	}
	forged := s
	forged.RunID = "run2"
	if res, _ := Verify(report, forged, pub); !res.HashOK || res.SigOK {
		t.Error("a changed claim must fail the signature")
	}
	other, _, _ := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	if res, _ := Verify(report, s, other); !res.SigOK || res.KeyMatches {
		t.Error("someone else's key must not match")
	}
	if res, _ := Verify(report, s, nil); res.KeyGiven || res.KeyMatches {
		t.Error("without a trusted key, the key can't match")
	}
}
