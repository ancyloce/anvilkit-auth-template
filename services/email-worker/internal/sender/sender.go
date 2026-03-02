package sender

import (
	"context"
	"errors"
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
		// Preserve historical zero-value Sender behavior: return SMTP config validation
		// errors instead of panicking when Sender is constructed directly.
		smtpClient = email.SMTPConfig{}
	}
	if err := smtpClient.SendEmail(req.To, req.Subject, req.HTMLBody, req.TextBody); err != nil {
		return "", err
	}
	return uuid.NewString(), nil
}
