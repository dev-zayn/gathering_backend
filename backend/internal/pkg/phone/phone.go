// Package phone keeps one canonical form of an Iraqi mobile number, +9647XXXXXXXXX,
// and formats it per consumer. A player may type any of these and reach the
// same account:
//
//	07701234567   7701234567   +9647701234567   009647701234567
package phone

import (
	"strings"

	"github.com/jamaa/gathering/backend/internal/pkg/errors"
)

const cc = "964"

// ErrInvalid is what every caller returns to the client. The field name is the
// caller's to choose, so this is the bare code.
var ErrInvalid = errors.BadRequest("invalid_phone")

// Normalize turns any accepted form into +9647XXXXXXXXX, or fails. It is the
// only way a number should ever enter the database.
func Normalize(in string) (string, error) {
	var d strings.Builder
	for _, r := range in {
		switch {
		case r >= '0' && r <= '9':
			d.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')':
			// Punctuation people type. Dropped, including the plus: the country
			// code is decided below from the digits alone.
		default:
			return "", ErrInvalid
		}
	}
	n := d.String()

	n = strings.TrimPrefix(n, "00")
	n = strings.TrimPrefix(n, cc)
	n = strings.TrimPrefix(n, "0")

	// What is left must be the national number: 7 and nine more digits.
	if len(n) != 10 || n[0] != '7' {
		return "", ErrInvalid
	}
	for i := 0; i < len(n); i++ {
		if n[i] < '0' || n[i] > '9' {
			return "", ErrInvalid
		}
	}
	return "+" + cc + n, nil
}

// Vendor is the form the OTP provider wants: the same number without the plus.
func Vendor(e164 string) string {
	return strings.TrimPrefix(e164, "+")
}

// Mask is for logs and support screens: +964770*****67.
func Mask(e164 string) string {
	if len(e164) < 8 {
		return "***"
	}
	return e164[:7] + strings.Repeat("*", len(e164)-9) + e164[len(e164)-2:]
}
