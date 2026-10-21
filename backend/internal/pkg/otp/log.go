package otp

import (
	"context"

	applog "github.com/jamaa/gathering/backend/internal/pkg/log"
	"github.com/jamaa/gathering/backend/internal/pkg/phone"
)

// LogSender writes the code to the log instead of sending it. Built only with
// DEBUG on and no webhook: production missing its gateway refuses to start.
type LogSender struct{}

func (LogSender) Send(ctx context.Context, e164, code string) error {
	applog.From(ctx).Warn("OTP NOT SENT — development sender",
		"phone", phone.Mask(e164), "code", code)
	return nil
}

func (LogSender) Notify(ctx context.Context, e164, text string) error {
	applog.From(ctx).Warn("MESSAGE NOT SENT — development sender",
		"phone", phone.Mask(e164), "text", text)
	return nil
}
