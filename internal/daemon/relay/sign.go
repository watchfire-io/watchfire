package relay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignaturePrefix is the algorithm tag prepended to every HMAC signature
// the v7.0 Relay webhook adapter emits. Receivers split on the first `=`
// and route to the matching verifier; future algorithm bumps land as
// additional accepted prefixes.
const SignaturePrefix = "sha256="

// SignHMAC computes an HMAC-SHA256 over body keyed on secret and returns
// the canonical `sha256=<hex>` signature header value the v7.0 Relay
// webhook adapter sets on `X-Watchfire-Signature`. Callers pass the raw
// secret bytes (the keyring fetch result) and the exact request body
// bytes so the receiver can re-sign byte-for-byte.
func SignHMAC(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMAC reports whether candidate matches the signature SignHMAC
// would produce for body under secret. Comparison is constant-time on
// the hex bytes so a timing-leak side channel cannot recover the secret
// from a probe loop. Returns false on any prefix / length / decode
// mismatch — the receiver is encouraged to treat that as a hard reject.
func VerifyHMAC(secret, body []byte, candidate string) bool {
	if len(candidate) <= len(SignaturePrefix) {
		return false
	}
	if candidate[:len(SignaturePrefix)] != SignaturePrefix {
		return false
	}
	want, err := hex.DecodeString(candidate[len(SignaturePrefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
