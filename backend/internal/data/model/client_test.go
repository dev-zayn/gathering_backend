package model

import (
	"strings"
	"testing"
)

func TestSecretIsHashedAndMatchedInConstantTime(t *testing.T) {
	secret := NewSecret()
	if !strings.HasPrefix(secret, "sec_") || len(secret) < 20 {
		t.Fatalf("secret = %q", secret)
	}

	hash := HashSecret(secret)
	if strings.Contains(hash, secret) {
		t.Fatal("the hash carries the secret")
	}
	if !SecretMatches(secret, hash) {
		t.Fatal("the right secret was refused")
	}
	for _, wrong := range []string{"", "sec_nope", secret + "x", strings.ToUpper(secret)} {
		if SecretMatches(wrong, hash) {
			t.Fatalf("%q was accepted", wrong)
		}
	}
	// A game with no secret set must not be openable with an empty one.
	if SecretMatches("", "") {
		t.Fatal("an empty secret opened a game with no secret")
	}
}

func TestTwoSecretsDiffer(t *testing.T) {
	a, b := NewSecret(), NewSecret()
	if a == b {
		t.Fatal("two secrets came out the same")
	}
}
