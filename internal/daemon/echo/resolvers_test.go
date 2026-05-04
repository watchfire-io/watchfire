package echo

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// TestResolveGitHubSecretFromKeyring covers the three branches of the
// resolver: keyring fetch error → propagated, empty value → friendly
// "not configured" error, populated value → returned as []byte.
func TestResolveGitHubSecretFromKeyring(t *testing.T) {
	t.Run("fetch error", func(t *testing.T) {
		fail := errors.New("keyring offline")
		got := ResolveGitHubSecretFromKeyring(func() (string, error) { return "", fail })
		if _, err := got(); !errors.Is(err, fail) {
			t.Fatalf("expected fetch error, got %v", err)
		}
	})
	t.Run("empty value", func(t *testing.T) {
		got := ResolveGitHubSecretFromKeyring(func() (string, error) { return "", nil })
		_, err := got()
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("expected 'not configured' error, got %v", err)
		}
	})
	t.Run("populated value", func(t *testing.T) {
		got := ResolveGitHubSecretFromKeyring(func() (string, error) { return "secret-bytes", nil })
		v, err := got()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if string(v) != "secret-bytes" {
			t.Fatalf("expected 'secret-bytes', got %q", v)
		}
	})
}

// TestResolveBitbucketSecretFromKeyring mirrors the GitHub test for the
// Bitbucket helper, which returns []byte for HMAC keying.
func TestResolveBitbucketSecretFromKeyring(t *testing.T) {
	t.Run("empty value", func(t *testing.T) {
		got := ResolveSecretFromKeyring(func() (string, error) { return "", nil })
		if _, err := got(); err == nil {
			t.Fatal("expected error on empty secret")
		}
	})
	t.Run("populated value", func(t *testing.T) {
		got := ResolveSecretFromKeyring(func() (string, error) { return "shared-secret", nil })
		v, err := got()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if string(v) != "shared-secret" {
			t.Fatalf("expected 'shared-secret', got %q", v)
		}
	})
	t.Run("fetch error", func(t *testing.T) {
		fail := errors.New("keychain locked")
		got := ResolveSecretFromKeyring(func() (string, error) { return "", fail })
		if _, err := got(); !errors.Is(err, fail) {
			t.Fatalf("expected fetch error, got %v", err)
		}
	})
}

// TestResolveSharedTokenFromKeyring covers the GitLab resolver, which
// returns string (not []byte) since GitLab compares the token verbatim
// against `X-Gitlab-Token`.
func TestResolveSharedTokenFromKeyring(t *testing.T) {
	t.Run("empty value", func(t *testing.T) {
		got := ResolveSharedTokenFromKeyring(func() (string, error) { return "", nil })
		if _, err := got(); err == nil {
			t.Fatal("expected error on empty token")
		}
	})
	t.Run("populated value", func(t *testing.T) {
		got := ResolveSharedTokenFromKeyring(func() (string, error) { return "tok", nil })
		v, err := got()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if v != "tok" {
			t.Fatalf("expected 'tok', got %q", v)
		}
	})
	t.Run("fetch error", func(t *testing.T) {
		fail := errors.New("keyring read failed")
		got := ResolveSharedTokenFromKeyring(func() (string, error) { return "", fail })
		if _, err := got(); !errors.Is(err, fail) {
			t.Fatalf("expected fetch error, got %v", err)
		}
	})
}

// TestResolvePublicKeyFromHex covers the Discord resolver, which decodes
// the hex-encoded public key into an ed25519.PublicKey for verification.
func TestResolvePublicKeyFromHex(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := hex.EncodeToString(pub)

	t.Run("empty value", func(t *testing.T) {
		got := ResolvePublicKeyFromHex(func() (string, error) { return "", nil })
		if _, err := got(); err == nil {
			t.Fatal("expected error on empty key")
		}
	})
	t.Run("populated value", func(t *testing.T) {
		got := ResolvePublicKeyFromHex(func() (string, error) { return encoded, nil })
		v, err := got()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if string(v) != string(pub) {
			t.Fatal("decoded key did not round-trip")
		}
	})
	t.Run("malformed hex", func(t *testing.T) {
		got := ResolvePublicKeyFromHex(func() (string, error) { return "zz", nil })
		if _, err := got(); err == nil {
			t.Fatal("expected error on malformed hex")
		}
	})
	t.Run("fetch error", func(t *testing.T) {
		fail := errors.New("keychain unreachable")
		got := ResolvePublicKeyFromHex(func() (string, error) { return "", fail })
		if _, err := got(); !errors.Is(err, fail) {
			t.Fatalf("expected fetch error, got %v", err)
		}
	})
}
