package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"log"
	"strings"
	texttemplate "text/template"
	"time"

	"anvilkit-auth-template/services/email-worker/internal/sender"
	emailtemplates "anvilkit-auth-template/services/email-worker/templates"
)

var (
	ErrNilQueue     = errors.New("nil_queue")
	ErrNilSender    = errors.New("nil_sender")
	ErrNilStore     = errors.New("nil_store")
	ErrEmptyQueue   = errors.New("empty_queue_name")
	ErrInvalidJob   = errors.New("invalid_email_job")
	ErrEmptyRecord  = errors.New("empty_record_id")
	ErrEmptyToEmail = errors.New("empty_to_email")
	ErrEmptyOTP     = errors.New("empty_otp")
	ErrEmptyMagic   = errors.New("empty_magic_link")
	ErrEmptyExpiry  = errors.New("empty_expires_in")
)

var (
	verificationHTMLTemplate = htmltemplate.Must(htmltemplate.ParseFS(emailtemplates.FS, "verification_email.html.tmpl"))
	verificationTextTemplate = texttemplate.Must(texttemplate.ParseFS(emailtemplates.FS, "verification_email.txt.tmpl"))
)

type Queue interface {
	DequeueIntoContext(ctx context.Context, queueName string, timeout time.Duration, out any) (bool, error)
}

type Sender interface {
	Send(ctx context.Context, req sender.Request) (string, error)
}

type Store interface {
	MarkSent(ctx context.Context, recordID, externalID string) error
	MarkFailed(ctx context.Context, recordID, reason string) error
}

type EmailJob struct {
	RecordID  string `json:"record_id"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	HTMLBody  string `json:"html_body"`
	TextBody  string `json:"text_body"`
	OTP       string `json:"otp"`
	MagicLink string `json:"magic_link"`
	ExpiresIn string `json:"expires_in"`
}

type Consumer struct {
	Queue     Queue
	QueueName string
	Timeout   time.Duration
	Sender    Sender
	Store     Store
}

func (c *Consumer) Run(ctx context.Context) error {
	if c.Queue == nil {
		return ErrNilQueue
	}
	if c.Sender == nil {
		return ErrNilSender
	}
	if c.Store == nil {
		return ErrNilStore
	}
	if strings.TrimSpace(c.QueueName) == "" {
		return ErrEmptyQueue
	}
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Second
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		var job EmailJob
		ok, err := c.Queue.DequeueIntoContext(ctx, c.QueueName, c.Timeout, &job)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if isPayloadDecodeError(err) {
				log.Printf("email-worker: dropped invalid payload from queue=%q: %v", c.QueueName, err)
				continue
			}
			return fmt.Errorf("dequeue email job: %w", err)
		}
		if !ok {
			continue
		}

		if err := c.handleJob(ctx, job); err != nil {
			log.Printf("email-worker: failed to process record_id=%q: %v", job.RecordID, err)
		}
	}
}

func (c *Consumer) handleJob(ctx context.Context, job EmailJob) error {
	if strings.TrimSpace(job.RecordID) == "" {
		return fmt.Errorf("%w: %v", ErrInvalidJob, ErrEmptyRecord)
	}
	if strings.TrimSpace(job.To) == "" {
		reason := fmt.Sprintf("%v: %v", ErrInvalidJob, ErrEmptyToEmail)
		if err := c.Store.MarkFailed(ctx, job.RecordID, reason); err != nil {
			return fmt.Errorf("%s; mark failed: %v", reason, err)
		}
		return errors.New(reason)
	}

	htmlBody := job.HTMLBody
	textBody := job.TextBody
	if strings.TrimSpace(htmlBody) == "" && strings.TrimSpace(textBody) == "" {
		renderedHTML, renderedText, err := renderVerificationEmailBody(job)
		if err != nil {
			reason := fmt.Sprintf("%v: %v", ErrInvalidJob, err)
			if markErr := c.Store.MarkFailed(ctx, job.RecordID, reason); markErr != nil {
				return fmt.Errorf("%s; mark failed: %v", reason, markErr)
			}
			return errors.New(reason)
		}
		htmlBody = renderedHTML
		textBody = renderedText
	}

	externalID, err := c.Sender.Send(ctx, sender.Request{
		To:       job.To,
		Subject:  job.Subject,
		HTMLBody: htmlBody,
		TextBody: textBody,
	})
	if err != nil {
		if markErr := c.Store.MarkFailed(ctx, job.RecordID, err.Error()); markErr != nil {
			return fmt.Errorf("send email: %w; mark failed: %v", err, markErr)
		}
		return err
	}

	if err := c.Store.MarkSent(ctx, job.RecordID, externalID); err != nil {
		return err
	}

	return nil
}

func renderVerificationEmailBody(job EmailJob) (string, string, error) {
	if strings.TrimSpace(job.OTP) == "" {
		return "", "", ErrEmptyOTP
	}
	if strings.TrimSpace(job.MagicLink) == "" {
		return "", "", ErrEmptyMagic
	}
	if strings.TrimSpace(job.ExpiresIn) == "" {
		return "", "", ErrEmptyExpiry
	}
	data := struct {
		OTP       string
		MagicLink string
		ExpiresIn string
	}{
		OTP:       job.OTP,
		MagicLink: job.MagicLink,
		ExpiresIn: job.ExpiresIn,
	}

	var htmlBody bytes.Buffer
	if err := verificationHTMLTemplate.Execute(&htmlBody, data); err != nil {
		return "", "", err
	}
	var textBody bytes.Buffer
	if err := verificationTextTemplate.Execute(&textBody, data); err != nil {
		return "", "", err
	}
	return htmlBody.String(), textBody.String(), nil
}

func isPayloadDecodeError(err error) bool {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntaxErr) || errors.As(err, &typeErr)
}
