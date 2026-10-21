package model

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
)

// CodeLength is what the player types. Six digits like every other Iraqi
// service: the protection is the attempt limit and the five-minute life.
const CodeLength = 6

// NewCode returns a fresh code. Leading zeros are kept: "004821" is a valid code
// and the client must treat it as text, never as a number.
func NewCode() string {
	max := big.NewInt(1)
	for i := 0; i < CodeLength; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		panic("model: no randomness available: " + err.Error())
	}

	s := n.String()
	for len(s) < CodeLength {
		s = "0" + s
	}
	return s
}

// HashCode is what gets stored, so reading Redis does not hand over the code. No
// salt and no slow hash: six digits with a five-minute life do not need one.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// CodeMatches compares in constant time, so a wrong code takes as long as a
// right one.
func CodeMatches(code, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashCode(code)), []byte(hash)) == 1
}
