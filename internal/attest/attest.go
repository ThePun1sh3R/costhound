// Package attest signs a costhound JSON report with the user's own ed25519
// key and checks such a signature. A signature proves one thing: these exact
// bytes are what the key's holder signed. The binary hash and the run ID it
// carries are claims, to check against the release checksums and CloudTrail.
package attest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Signature is the .sig file written next to a report.
type Signature struct {
	Format       string    `json:"format"`        // "costhound-report-signature/1"
	ReportSHA256 string    `json:"report_sha256"` // of the report file's exact bytes
	Version      string    `json:"costhound_version"`
	BinarySHA256 string    `json:"binary_sha256"` // claimed: check it against the release's SHA256SUMS
	RunID        string    `json:"run_id,omitempty"`
	SignedAt     time.Time `json:"signed_at"`  // claimed: the signer's clock
	PublicKey    string    `json:"public_key"` // base64; trust it only if it matches the key you got from the signer
	Sig          string    `json:"signature"`  // base64 ed25519 over Message()
}

const format = "costhound-report-signature/1"

// Message is what the signature covers: every field but the signature.
func (s Signature) Message() []byte {
	return []byte(strings.Join([]string{s.Format, s.ReportSHA256, s.Version, s.BinarySHA256, s.RunID,
		s.SignedAt.UTC().Format(time.RFC3339), s.PublicKey}, "\n"))
}

// Result is what Verify could establish.
type Result struct {
	HashOK, SigOK bool
	KeyMatches    bool // the signing key is the one the verifier passed (false when none was passed)
	KeyGiven      bool // the verifier passed a key to check against
	Sig           Signature
}

// Verify checks report against its signature, and against pub when given.
func Verify(report []byte, s Signature, pub ed25519.PublicKey) (Result, error) {
	res := Result{Sig: s, KeyGiven: pub != nil}
	if s.Format != format {
		return res, fmt.Errorf("not a costhound report signature (format %q)", s.Format)
	}
	sum := sha256.Sum256(report)
	res.HashOK = hex.EncodeToString(sum[:]) == s.ReportSHA256
	key, err := base64.StdEncoding.DecodeString(s.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return res, errors.New("the signature's public key is malformed")
	}
	sig, err := base64.StdEncoding.DecodeString(s.Sig)
	if err != nil {
		return res, errors.New("the signature is malformed")
	}
	res.SigOK = ed25519.Verify(ed25519.PublicKey(key), s.Message(), sig)
	res.KeyMatches = pub != nil && pub.Equal(ed25519.PublicKey(key))
	return res, nil
}

// Keys: the private key file holds the 64-byte ed25519 key in base64; the
// public key file holds the 32-byte key in base64, to hand to whoever checks.

// GenerateKey writes a new key pair (private 0600, refusing to overwrite).
func GenerateKey(privPath, pubPath string, random io.Reader) (ed25519.PublicKey, error) {
	if random == nil {
		random = rand.Reader
	}
	pub, priv, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return nil, err
	}
	if err := writeNew(privPath, base64.StdEncoding.EncodeToString(priv)+"\n", 0o600); err != nil {
		return nil, err
	}
	if err := writeNew(pubPath, base64.StdEncoding.EncodeToString(pub)+"\n", 0o644); err != nil {
		return nil, err
	}
	return pub, nil
}

func writeNew(path, body string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists: costhound never overwrites a key", path)
		}
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ParsePublic reads a public key: a file path, or the base64 key itself.
func ParsePublic(v string) (ed25519.PublicKey, error) {
	if b, err := os.ReadFile(v); err == nil {
		v = string(b)
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, errors.New("not an ed25519 public key (a file, or the base64 key)")
	}
	return ed25519.PublicKey(k), nil
}
