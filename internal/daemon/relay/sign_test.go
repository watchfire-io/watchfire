package relay

import "testing"

// TestSignHMACGolden locks the canonical `sha256=<hex>` shape against a
// hand-computed reference so the format never silently drifts. Same
// secret + body always produces the same hex digest, and the prefix is
// always `sha256=`.
func TestSignHMACGolden(t *testing.T) {
	secret := []byte("super-secret-shhh")
	body := []byte(`{"version":1,"kind":"TASK_FAILED"}`)
	// Computed offline once; locked in here so a regression in the
	// signing path is caught at compile-time, not in production.
	const want = "sha256=4f53e11acdd89e922f7790bcb481be183414fde9421a9f61ad501f82f7ea9821"
	got := SignHMAC(secret, body)
	if got != want {
		t.Fatalf("SignHMAC golden mismatch:\n got:  %s\n want: %s", got, want)
	}
}

func TestSignHMACDeterministic(t *testing.T) {
	secret := []byte("k")
	body := []byte("payload")
	a := SignHMAC(secret, body)
	b := SignHMAC(secret, body)
	if a != b {
		t.Fatalf("SignHMAC not deterministic: %q vs %q", a, b)
	}
}

func TestSignHMACDiffersOnSecretAndBody(t *testing.T) {
	base := SignHMAC([]byte("k1"), []byte("body"))
	if SignHMAC([]byte("k2"), []byte("body")) == base {
		t.Fatal("expected different signature for different secret")
	}
	if SignHMAC([]byte("k1"), []byte("BODY")) == base {
		t.Fatal("expected different signature for different body")
	}
	if SignHMAC([]byte("k1"), []byte("")) == base {
		t.Fatal("expected different signature for empty body")
	}
}

func TestSignHMACEmptyInputsStillSign(t *testing.T) {
	got := SignHMAC([]byte{}, []byte{})
	// HMAC-SHA256 of empty body with empty key — locked golden.
	const want = "sha256=b613679a0814d9ec772f95d778c35fc5ff1697c493715653c6c712144292c5ad"
	if got != want {
		t.Fatalf("empty-input golden mismatch:\n got:  %s\n want: %s", got, want)
	}
}

func TestVerifyHMACRoundTrip(t *testing.T) {
	secret := []byte("k")
	body := []byte("payload")
	sig := SignHMAC(secret, body)
	if !VerifyHMAC(secret, body, sig) {
		t.Fatal("VerifyHMAC rejected its own signature")
	}
}

func TestVerifyHMACRejectsWrongInputs(t *testing.T) {
	secret := []byte("k")
	body := []byte("payload")
	good := SignHMAC(secret, body)

	cases := map[string]struct {
		secret []byte
		body   []byte
		sig    string
	}{
		"wrong secret": {[]byte("not-k"), body, good},
		"wrong body":   {secret, []byte("BODY"), good},
		"empty sig":    {secret, body, ""},
		"missing prefix": {
			secret, body,
			good[len(SignaturePrefix):],
		},
		"bad prefix":   {secret, body, "md5=00ff"},
		"bad hex":      {secret, body, "sha256=not-hex"},
		"wrong length": {secret, body, "sha256=00"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if VerifyHMAC(c.secret, c.body, c.sig) {
				t.Fatalf("expected verify to fail for %s, got accept", name)
			}
		})
	}
}
