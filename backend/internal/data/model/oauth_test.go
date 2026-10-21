package model

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestVerifyPKCE(t *testing.T) {
	verifier := strings.Repeat("a", 64)
	challenge := challengeFor(verifier)

	if !VerifyPKCE(challenge, verifier) {
		t.Fatal("the right verifier was refused")
	}
	if VerifyPKCE(challenge, strings.Repeat("b", 64)) {
		t.Fatal("the wrong verifier was accepted")
	}
	if VerifyPKCE(challenge, "") {
		t.Fatal("an empty verifier answered a real challenge")
	}
	// "plain" is a challenge that is its own answer, so a client sending the
	// challenge back as the verifier must not pass.
	if VerifyPKCE(challenge, challenge) {
		t.Fatal("the challenge was accepted as its own verifier")
	}
}

// No challenge is the in-app launch, where the code never leaves the app. It
// must still refuse a caller who invents a verifier for it.
func TestVerifyPKCEWithoutAChallenge(t *testing.T) {
	if !VerifyPKCE("", "") {
		t.Fatal("the in-app launch was refused")
	}
	if VerifyPKCE("", "anything") {
		t.Fatal("a verifier was accepted where no challenge was set")
	}
}

func TestHashCredentialIsStableAndNotThePlaintext(t *testing.T) {
	h := HashCredential("cod_abc")
	if h == "cod_abc" || strings.Contains(h, "cod_abc") {
		t.Fatal("the hash carries the plaintext")
	}
	if h != HashCredential("cod_abc") {
		t.Fatal("hashing the same value twice gave two answers")
	}
	if h == HashCredential("cod_abd") {
		t.Fatal("two different credentials hash the same")
	}
}

func TestCodeSpendable(t *testing.T) {
	now := time.Now()
	used := now.Add(-time.Second)

	fresh := &Code{ExpiresAt: now.Add(time.Minute)}
	if !fresh.Spendable(now) {
		t.Fatal("a fresh code was refused")
	}

	spent := &Code{ExpiresAt: now.Add(time.Minute), UsedAt: &used}
	if spent.Spendable(now) {
		t.Fatal("a spent code was spendable again")
	}

	expired := &Code{ExpiresAt: now.Add(-time.Second)}
	if expired.Spendable(now) {
		t.Fatal("an expired code was spendable")
	}
}

// Effective is the whole security model in one function: an elevated scope
// needs a person present, and everything else passes through.
func TestGrantEffective(t *testing.T) {
	grant := &Grant{Scopes: []Scope{
		{Key: "identity:read"},
		{Key: "friends:read"},
		{Key: "results:write", Elevated: true},
	}}

	cases := []struct {
		name string
		live bool
		want []string
	}{
		{"present", true, []string{"identity:read", "friends:read", "results:write"}},
		{"renewed with nobody there", false, []string{"identity:read", "friends:read"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grant.Effective(tc.live)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("Effective(%v) = %v, want %v", tc.live, got, tc.want)
			}
		})
	}
}

func TestGrantEffectiveIsNeverNil(t *testing.T) {
	// A JSON scope list must serialise as [] and not null, or a client reading
	// the length of it crashes on a game that was granted nothing.
	got := (&Grant{}).Effective(true)
	if got == nil {
		t.Fatal("an empty grant gave a nil slice")
	}
}
