package fcm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Error is Firebase refusing a message: the HTTP status and Firebase's own
// code and words for why, with the message's token cut out of them.
type Error struct {
	Status int

	// Code is Firebase's error code (UNREGISTERED, INVALID_ARGUMENT,
	// QUOTA_EXCEEDED, UNAVAILABLE, SENDER_ID_MISMATCH…), else the API's
	// status (NOT_FOUND, PERMISSION_DENIED…); empty when the answer was not
	// a Google API's at all, as from a wrong endpoint.
	Code    string
	Message string

	// google is an answer in the Google API's error shape; token is one
	// whose field violations, or words, name the message's token.
	google bool
	token  bool
}

func (e *Error) Error() string {
	s := fmt.Sprintf("fcm: %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Gone says Firebase will never deliver to this token: the app was removed
// or the token expired (UNREGISTERED, or a 404 for the token), or it was
// never a token (INVALID_ARGUMENT naming it). Its phone should be forgotten.
// A 404 that is not the API's error (a wrong endpoint) is not one.
func (e *Error) Gone() bool {
	switch {
	case e.Code == "UNREGISTERED":
		return true
	case e.Status == http.StatusNotFound && e.google:
		return true
	case e.Code == "INVALID_ARGUMENT" && e.token:
		return true
	}
	return false
}

// Temporary says the same message may go through later: Firebase is over
// its quota (429) or failing (5xx).
func (e *Error) Temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// apiError is how a Google API says no.
type apiError struct {
	Error struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			ErrorCode       string `json:"errorCode"`
			FieldViolations []struct {
				Field string `json:"field"`
			} `json:"fieldViolations"`
		} `json:"details"`
	} `json:"error"`
}

// maxMessage keeps a refusal's words to a log line.
const maxMessage = 300

// refusal reads a non-2xx answer. token is cut out of whatever Firebase said,
// so the error can be logged as it is.
func refusal(status int, body []byte, token string) *Error {
	e := &Error{Status: status}
	var api apiError
	if json.Unmarshal(body, &api) != nil || api.Error.Status == "" {
		return e
	}
	e.google = true
	e.Code = api.Error.Status
	for _, d := range api.Error.Details {
		if d.ErrorCode != "" {
			e.Code = d.ErrorCode
		}
		for _, v := range d.FieldViolations {
			if v.Field == "message.token" {
				e.token = true
			}
		}
	}
	msg := api.Error.Message
	if strings.Contains(strings.ToLower(msg), "registration token") {
		e.token = true
	}
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "…")
	}
	if len(msg) > maxMessage {
		n := maxMessage
		for n > 0 && !utf8.RuneStart(msg[n]) {
			n--
		}
		msg = msg[:n] + "…"
	}
	e.Message = msg
	return e
}
