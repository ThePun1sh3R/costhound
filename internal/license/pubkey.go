package license

import (
	"crypto/ed25519"
	"encoding/base64"
)

// publicKeyB64 is the public half of the seller's signing key
// (costhound-license pubkey). Changing it invalidates every license issued so far.
const publicKeyB64 = "1ZyqgR1bUGKrXs8TMHUddla975qrZGX1cRQXaXd/46c="

// PublicKey verifies license keys. A broken constant gives nil, which rejects
// every key; TestEmbeddedPublicKey guards against shipping that.
var PublicKey = parsePublicKey(publicKeyB64)

func parsePublicKey(s string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}
