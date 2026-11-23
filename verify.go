package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ThePun1sh3R/costhound/internal/attest"
	"github.com/ThePun1sh3R/costhound/internal/report"
)

// keygen makes the key that signs reports (costhound -sign): the private
// half stays with the user, the public half goes to whoever checks.
func keygen(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("costhound keygen", flag.ContinueOnError)
	dir := fs.String("dir", "", "where to write report-signing.key and report-signing.pub (default: costhound in your config folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		d, err := defaultKeyDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
			return 1
		}
		*dir = d
	}
	priv, pub := filepath.Join(*dir, "report-signing.key"), filepath.Join(*dir, "report-signing.pub")
	key, err := attest.GenerateKey(priv, pub, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Signing key: %s (keep it private)\nPublic key:  %s, fingerprint %s\n"+
		"Give the public key to whoever checks your reports (costhound verify -key), by a channel they trust.\n",
		priv, pub, fingerprint(key))
	return 0
}

func defaultKeyDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "costhound"), nil
}

func fingerprint(k ed25519.PublicKey) string {
	sum := sha256.Sum256(k)
	return hex.EncodeToString(sum[:8])
}

// verify checks a signed report (costhound -sign) and says in plain words
// what the signature proves and what it can't.
func verify(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("costhound verify", flag.ContinueOnError)
	keyArg := fs.String("key", "", "the signer's public key (a file, or the base64 key) that you got from them by a channel you trust")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: costhound verify [-key <public key>] <report.json>   (the signature is <report.json>.sig)")
		return 2
	}
	path := fs.Arg(0)
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 1
	}
	sb, err := os.ReadFile(path + ".sig")
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: no signature next to the report: %v\n", err)
		return 1
	}
	var sig attest.Signature
	if err := json.Unmarshal(sb, &sig); err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %s.sig is not a costhound signature\n", path)
		return 1
	}
	var pub ed25519.PublicKey
	if *keyArg != "" {
		if pub, err = attest.ParsePublic(*keyArg); err != nil {
			fmt.Fprintf(os.Stderr, "costhound: -key: %v\n", err)
			return 2
		}
	}
	res, err := attest.Verify(body, sig, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "costhound: %v\n", err)
		return 1
	}
	p := func(format string, a ...any) { fmt.Fprintf(stdout, format, a...) }
	switch {
	case !res.HashOK:
		p("FAILED: %s has changed since it was signed (its SHA-256 doesn't match the signature).\n", path)
		return 1
	case !res.SigOK:
		p("FAILED: the signature doesn't match its own claims or key: %s.sig was altered or forged.\n", path)
		return 1
	case res.KeyGiven && !res.KeyMatches:
		p("FAILED: the report was signed, but not with the key you gave (signed by %s).\n", keyFingerprint(sig.PublicKey))
		return 1
	}
	p("OK: %s is exactly what the holder of key %s signed.\n", path, keyFingerprint(sig.PublicKey))
	if !res.KeyGiven {
		p("  The key was NOT checked: anyone can sign with a key of their own. Pass -key with the signer's public key, got from them directly.\n")
	}
	p("\nClaims to check yourself:\n")
	p("  made by costhound %s, binary SHA-256 %s: compare with that release's SHA256SUMS\n", sig.Version, orUnknown(sig.BinarySHA256))
	if sig.RunID != "" {
		p("  run %s: the account's CloudTrail can confirm this run changed nothing (the text report prints the command)\n", sig.RunID)
	}
	p("  signed %s, by the signer's clock\n", sig.SignedAt.Format("2006-01-02 15:04 UTC"))
	var r report.Report
	if json.Unmarshal(body, &r) == nil && r.Tool == "costhound" {
		p("\nWhat a signature can't tell you: that the scan saw everything, or that a finding is right.\n")
		p("  This report: %d region(s), %d region(s) blocked, %d call(s) it couldn't make", len(r.Regions), len(r.BlockedRegions), len(r.Skipped))
		if r.Run != nil {
			if _, denied, _ := r.Run.Totals(); denied > 0 {
				p(", %d denied", denied)
			}
		}
		p(".\n")
		if r.Redaction != nil {
			p("  It is a redacted copy; the full report's SHA-256 is %s (the file -redact-full saved).\n", r.Redaction.Commitment)
		}
	}
	return 0
}

func keyFingerprint(b64 string) string {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return "(malformed)"
	}
	return fingerprint(ed25519.PublicKey(k))
}

func orUnknown(s string) string {
	if s == "" {
		return "(not recorded)"
	}
	return s
}
