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

type Sender struct {
	smtp email.SMTPConfig
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
	if err := s.smtp.SendEmail(req.To, req.Subject, req.HTMLBody, req.TextBody); err != nil {
		return "", err
	}
	return uuid.NewString(), nil
}
