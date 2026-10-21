// Package logstore is the platform's log: the audit trail. It lives in the logs
// schema of the same Postgres, on a pool of its own, so a burst of log writes
// cannot take the connections players need.
package logstore

import (
	"strings"
	"unicode/utf8"
)

const maxLimit = 200

// LimitOf is the page size actually used for a requested one.
func LimitOf(requested int) int {
	if requested <= 0 || requested > maxLimit {
		return 50
	}
	return requested
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// clip cuts a value to its column, counted in characters as varchar counts them.
// Some come from outside, and a line cut short beats a line not written.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// contains is the ILIKE pattern for a substring, with the term's own wildcards
// escaped: somebody searching for "50%" means the characters.
func contains(term string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(term) + "%"
}
