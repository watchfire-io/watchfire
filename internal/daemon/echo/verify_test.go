package echo

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestVerifyGitHub(t *testing.T) {
	secret := []byte("watchfire-github-secret")
	body := []byte(`{"action":"closed","pull_request":{"merged":true}}`)

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	t.Run("valid signature", func(t *testing.T) {
		if err := VerifyGitHub(secret, body, good); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		if err := VerifyGitHub(secret, []byte(`{"tampered":true}`), good); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got %v", err)
		}
	})
	t.Run("wrong secret", func(t *testing.T) {
		if err := VerifyGitHub([]byte("nope"), body, good); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got %v", err)
		}
	})
	t.Run("malformed prefix", func(t *testing.T) {
		if err := VerifyGitHub(secret, body, "md5=abc"); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("malformed hex", func(t *testing.T) {
		if err := VerifyGitHub(secret, body, "sha256=zzz"); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("empty secret", func(t *testing.T) {
		if err := VerifyGitHub(nil, body, good); !errors.Is(err, ErrEmptySecret) {
			t.Fatalf("expected ErrEmptySecret, got %v", err)
		}
	})
}

func TestVerifySlack(t *testing.T) {
	secret := []byte("slack-signing-secret")
	body := []byte(`token=abc&team_id=T0&command=/watchfire&text=status`)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })

	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	good := "v0=" + hex.EncodeToString(mac.Sum(nil))

	t.Run("valid signature", func(t *testing.T) {
		if err := VerifySlack(secret, ts, body, good); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("expired timestamp", func(t *testing.T) {
		stale := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte("v0:" + stale + ":"))
		mac.Write(body)
		sig := "v0=" + hex.EncodeToString(mac.Sum(nil))
		if err := VerifySlack(secret, stale, body, sig); !errors.Is(err, ErrTimestampDrift) {
			t.Fatalf("expected ErrTimestampDrift, got %v", err)
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		if err := VerifySlack(secret, ts, []byte("tampered"), good); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got %v", err)
		}
	})
	t.Run("malformed timestamp", func(t *testing.T) {
		if err := VerifySlack(secret, "not-a-number", body, good); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("malformed prefix", func(t *testing.T) {
		if err := VerifySlack(secret, ts, body, "v9=abc"); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("empty secret", func(t *testing.T) {
		if err := VerifySlack(nil, ts, body, good); !errors.Is(err, ErrEmptySecret) {
			t.Fatalf("expected ErrEmptySecret, got %v", err)
		}
	})
}

func TestVerifyDiscord(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	body := []byte(`{"type":1,"id":"123","application_id":"456"}`)
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	SetClockForTest(func() time.Time { return now })
	t.Cleanup(func() { SetClockForTest(nil) })
	ts := strconv.FormatInt(now.Unix(), 10)

	signed := ed25519.Sign(priv, append([]byte(ts), body...))
	sigHex := hex.EncodeToString(signed)

	t.Run("valid signature", func(t *testing.T) {
		if err := VerifyDiscord(pub, []byte(ts), body, sigHex); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		if err := VerifyDiscord(pub, []byte(ts), []byte(`{"tampered":true}`), sigHex); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got %v", err)
		}
	})
	t.Run("expired timestamp", func(t *testing.T) {
		stale := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
		signedStale := ed25519.Sign(priv, append([]byte(stale), body...))
		if err := VerifyDiscord(pub, []byte(stale), body, hex.EncodeToString(signedStale)); !errors.Is(err, ErrTimestampDrift) {
			t.Fatalf("expected ErrTimestampDrift, got %v", err)
		}
	})
	t.Run("malformed hex", func(t *testing.T) {
		if err := VerifyDiscord(pub, []byte(ts), body, "zz"); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("wrong-length signature", func(t *testing.T) {
		if err := VerifyDiscord(pub, []byte(ts), body, hex.EncodeToString([]byte("short"))); !errors.Is(err, ErrMalformedHeader) {
			t.Fatalf("expected ErrMalformedHeader, got %v", err)
		}
	})
	t.Run("empty key", func(t *testing.T) {
		if err := VerifyDiscord(nil, []byte(ts), body, sigHex); !errors.Is(err, ErrEmptySecret) {
			t.Fatalf("expected ErrEmptySecret, got %v", err)
		}
	})
	t.Run("non-numeric timestamp passes through to sig check", func(t *testing.T) {
		// Sign with a non-numeric timestamp string — VerifyDiscord
		// should skip the replay window and rely entirely on the
		// signature check.
		const ts2 = "not-a-unix-timestamp"
		signed := ed25519.Sign(priv, append([]byte(ts2), body...))
		if err := VerifyDiscord(pub, []byte(ts2), body, hex.EncodeToString(signed)); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
}

func TestDecodeDiscordPublicKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	encoded := hex.EncodeToString(pub)

	got, err := DecodeDiscordPublicKey(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got) != string(pub) {
		t.Fatalf("round trip mismatch")
	}

	if _, err := DecodeDiscordPublicKey("zz"); err == nil {
		t.Fatalf("expected error on bad hex")
	}
	if _, err := DecodeDiscordPublicKey(hex.EncodeToString([]byte("short"))); err == nil {
		t.Fatalf("expected error on short key")
	}
}
