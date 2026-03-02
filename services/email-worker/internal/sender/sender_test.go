package sender

import (
	"context"
	"errors"
	"testing"

	commonemail "anvilkit-auth-template/modules/common-go/pkg/email"
	"github.com/google/uuid"
)

type smtpCall struct {
	to       string
	subject  string
	htmlBody string
	textBody string
}

type mockSMTP struct {
	err   error
	calls []smtpCall
}

func (m *mockSMTP) SendEmail(to, subject, htmlBody, textBody string) error {
	m.calls = append(m.calls, smtpCall{
		to:       to,
		subject:  subject,
		htmlBody: htmlBody,
		textBody: textBody,
	})
	return m.err
}

func TestSend_SuccessReturnsUUIDAndForwardsRequest(t *testing.T) {
	smtp := &mockSMTP{}
	s := &Sender{smtp: smtp}

	id, err := s.Send(context.Background(), Request{
		To:       "user@example.com",
		Subject:  "Verify login",
		HTMLBody: "<p>Hello</p>",
		TextBody: "Hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("id=%q is not a valid UUID: %v", id, err)
	}
	if len(smtp.calls) != 1 {
		t.Fatalf("smtp calls=%d want=1", len(smtp.calls))
	}
	got := smtp.calls[0]
	if got.to != "user@example.com" || got.subject != "Verify login" || got.htmlBody != "<p>Hello</p>" || got.textBody != "Hello" {
		t.Fatalf("unexpected smtp call: %+v", got)
	}
}

func TestSend_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	smtp := &mockSMTP{}
	s := &Sender{smtp: smtp}

	if _, err := s.Send(ctx, Request{To: "user@example.com"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want=%v", err, context.Canceled)
	}
	if len(smtp.calls) != 0 {
		t.Fatalf("smtp calls=%d want=0", len(smtp.calls))
	}
}

func TestSend_EmptyRecipient(t *testing.T) {
	smtp := &mockSMTP{}
	s := &Sender{smtp: smtp}

	if _, err := s.Send(context.Background(), Request{To: "   "}); !errors.Is(err, ErrEmptyRecipient) {
		t.Fatalf("err=%v want=%v", err, ErrEmptyRecipient)
	}
	if len(smtp.calls) != 0 {
		t.Fatalf("smtp calls=%d want=0", len(smtp.calls))
	}
}

func TestSend_SMTPError(t *testing.T) {
	smtp := &mockSMTP{err: errors.New("smtp rejected message")}
	s := &Sender{smtp: smtp}

	id, err := s.Send(context.Background(), Request{
		To:      "user@example.com",
		Subject: "Subject",
	})
	if err == nil || err.Error() != "smtp rejected message" {
		t.Fatalf("err=%v want=%q", err, "smtp rejected message")
	}
	if id != "" {
		t.Fatalf("id=%q want empty", id)
	}
	if len(smtp.calls) != 1 {
		t.Fatalf("smtp calls=%d want=1", len(smtp.calls))
	}
}

func TestSend_ZeroValueSenderReturnsSMTPValidationError(t *testing.T) {
	s := &Sender{}

	id, err := s.Send(context.Background(), Request{
		To:      "user@example.com",
		Subject: "Subject",
	})
	if !errors.Is(err, commonemail.ErrEmptySMTPHost) {
		t.Fatalf("err=%v want=%v", err, commonemail.ErrEmptySMTPHost)
	}
	if id != "" {
		t.Fatalf("id=%q want empty", id)
	}
}
