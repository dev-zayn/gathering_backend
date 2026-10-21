// Package errors carries the one failure shape the api returns:
// a code, and only the extra keys that code actually needs.
//
//	{ "error": "phone_taken" }
//	{ "error": "invalid_fields", "fields": { "business.district": "unknown_district" } }
//	{ "error": "too_many_requests", "retry_after": 45 }
package errors

import "net/http"

type Error struct {
	Status     int               `json:"-"`
	Code       string            `json:"error"`
	Fields     map[string]string `json:"fields,omitempty"`
	RetryAfter int               `json:"retry_after,omitempty"`

	// AttemptsLeft is a pointer so that zero — the last guess spent — still
	// reaches the client instead of being omitted.
	AttemptsLeft *int `json:"attempts_left,omitempty"`

	// State is the status explaining the refusal. Named State because Status above
	// is the HTTP code.
	State string `json:"status,omitempty"`

	// Missing is what a submission still needs, in the client's own dotted
	// field names.
	Missing []string `json:"missing,omitempty"`

	// Scope names the one permission a caller was not granted. A game's server reads
	// it to know which scope to ask for, rather than guessing.
	Scope string `json:"scope,omitempty"`

	// Cause never reaches the client. It is what gets logged.
	Cause error `json:"-"`
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

// Wrap attaches the underlying failure for the logs without changing what the
// client sees.
func (e *Error) Wrap(cause error) *Error {
	c := *e
	c.Cause = cause
	return &c
}

func New(status int, code string) *Error {
	return &Error{Status: status, Code: code}
}

func BadRequest(code string) *Error   { return New(http.StatusBadRequest, code) }
func Unauthorized(code string) *Error { return New(http.StatusUnauthorized, code) }
func Forbidden(code string) *Error    { return New(http.StatusForbidden, code) }
func NotFound(code string) *Error     { return New(http.StatusNotFound, code) }
func Conflict(code string) *Error     { return New(http.StatusConflict, code) }

// Invalid is the one error that carries a per-field map. Keys are the dotted
// path of the field as the client sent it: "business.district".
func Invalid(fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_fields", Fields: fields}
}

func TooManyRequests(retryAfterSeconds int) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "too_many_requests", RetryAfter: retryAfterSeconds}
}

func Internal(cause error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Cause: cause}
}

// Upstream is a dependency of ours failing, not the client's fault.
func Upstream(code string, cause error) *Error {
	return &Error{Status: http.StatusBadGateway, Code: code, Cause: cause}
}

// Locked refuses a write because of the state the thing is in, and says which
// state that is — "ask an admin" and "wait for review" are different answers.
func Locked(code, state string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, State: state}
}

// Incomplete lists what is still missing rather than naming the first gap, so
// the player fills it in once.
func Incomplete(missing []string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "incomplete", Missing: missing}
}

// ScopeRequired refuses a call and names the scope that would have opened it, so
// a partner does not have to guess which one they are missing.
func ScopeRequired(scope string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "scope_required", Scope: scope}
}

// WrongCode is the one failure that tells the client how much room is left. At
// zero the code is gone and signing in starts over.
func WrongCode(attemptsLeft int) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "wrong_code", AttemptsLeft: &attemptsLeft}
}
