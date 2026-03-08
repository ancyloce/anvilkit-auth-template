package sender

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"anvilkit-auth-template/modules/common-go/pkg/email"
)

var ErrEmptyRecipient = errors.New("empty_recipient")

type Request struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}

type smtpClient interface {
	SendEmail(to, subject, htmlBody, textBody string) error
}

type DeliveryError struct {
	Cause          error
	Classification BounceClassification
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Cause == nil {
		return "delivery_error"
	}
	if e.Classification.Type == BounceTypeNone {
		return e.Cause.Error()
	}
	return fmt.Sprintf("%s (bounce=%s code=%d)", e.Cause.Error(), e.Classification.Type, e.Classification.SMTPCode)
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type Sender struct {
	smtp smtpClient
}

func New(cfg email.SMTPConfig) *Sender {
	return &Sender{smtp: cfg}
}

func (s *Sender) Send(ctx context.Context, req Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.To) == "" {
		return "", ErrEmptyRecipient
	}
	smtpClient := s.smtp
	if smtpClient == nil {
		smtpClient = email.SMTPConfig{}
	}
	if err := smtpClient.SendEmail(req.To, req.Subject, req.HTMLBody, req.TextBody); err != nil {
		return "", &DeliveryError{Cause: err, Classification: ClassifySMTPError(err)}
	}
	return uuid.NewString(), nil
}
