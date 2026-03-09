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

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	"anvilkit-auth-template/services/email-worker/internal/monitoring"
	"anvilkit-auth-template/services/email-worker/internal/sender"
	workerstore "anvilkit-auth-template/services/email-worker/internal/store"
	emailtemplates "anvilkit-auth-template/services/email-worker/templates"
)

var (
	ErrNilQueue           = errors.New("nil_queue")
	ErrNilSender          = errors.New("nil_sender")
	ErrNilStore           = errors.New("nil_store")
	ErrEmptyQueue         = errors.New("empty_queue_name")
	ErrInvalidJob         = errors.New("invalid_email_job")
	ErrEmptyRecord        = errors.New("empty_record_id")
	ErrEmptyToEmail       = errors.New("empty_to_email")
	ErrEmptyOTP           = errors.New("empty_otp")
	ErrEmptyMagic         = errors.New("empty_magic_link")
	ErrEmptyExpiry        = errors.New("empty_expires_in")
	ErrEmailBlacklisted   = errors.New("email_blacklisted")
	ErrSoftBounceExceeded = errors.New("soft_bounce_retry_exhausted")
)

var (
	verificationHTMLTemplate = htmltemplate.Must(htmltemplate.ParseFS(emailtemplates.FS, "verification_email.html.tmpl"))
	verificationTextTemplate = texttemplate.Must(texttemplate.ParseFS(emailtemplates.FS, "verification_email.txt.tmpl"))
	softBounceRetryIntervals = []time.Duration{time.Hour, 4 * time.Hour, 24 * time.Hour}
)

type Queue interface {
	DequeueIntoContext(ctx context.Context, queueName string, timeout time.Duration, out any) (bool, error)
	EnqueueContext(ctx context.Context, queueName string, payload any) error
}

type Sender interface {
	Send(ctx context.Context, req sender.Request) (string, error)
}

type Store interface {
	MarkSent(ctx context.Context, recordID, externalID string) error
	MarkFailed(ctx context.Context, recordID, reason string) error
	MarkBounced(ctx context.Context, recordID, reason, bounceType string, smtpCode, retryCount int) error
	Blacklist(ctx context.Context, emailAddr, reason string) error
	IsBlacklisted(ctx context.Context, emailAddr string) (bool, error)
	LookupAnalyticsRecordByID(ctx context.Context, recordID string) (*workerstore.AnalyticsRecord, error)
}

type Scheduler interface {
	AfterFunc(d time.Duration, f func())
}

type realScheduler struct{}

func (realScheduler) AfterFunc(d time.Duration, f func()) {
	time.AfterFunc(d, f)
}

type EmailJob struct {
	RecordID   string `json:"record_id"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	HTMLBody   string `json:"html_body"`
	TextBody   string `json:"text_body"`
	OTP        string `json:"otp"`
	MagicLink  string `json:"magic_link"`
	ExpiresIn  string `json:"expires_in"`
	RetryCount int    `json:"retry_count,omitempty"`
}

type Consumer struct {
	Queue     Queue
	QueueName string
	Timeout   time.Duration
	Sender    Sender
	Store     Store
	Analytics analytics.Client
	Scheduler Scheduler
	Metrics   *monitoring.Metrics
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
	if c.Scheduler == nil {
		c.Scheduler = realScheduler{}
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
	isBlacklisted, err := c.Store.IsBlacklisted(ctx, job.To)
	if err != nil {
		return err
	}
	if isBlacklisted {
		reason := ErrEmailBlacklisted.Error()
		if markErr := c.Store.MarkFailed(ctx, job.RecordID, reason); markErr != nil {
			return fmt.Errorf("%s; mark failed: %v", reason, markErr)
		}
		return ErrEmailBlacklisted
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

	startedAt := time.Now()
	externalID, err := c.Sender.Send(ctx, sender.Request{To: job.To, Subject: job.Subject, HTMLBody: htmlBody, TextBody: textBody})
	if err != nil {
		if c.Metrics != nil {
			c.Metrics.ObserveSendFailure(time.Since(startedAt))
		}
		return c.handleDeliveryError(ctx, job, err)
	}
	if c.Metrics != nil {
		c.Metrics.ObserveSendSuccess(time.Since(startedAt))
	}

	if err := c.Store.MarkSent(ctx, job.RecordID, externalID); err != nil {
		return err
	}
	c.trackVerificationEmailSent(ctx, job.RecordID)

	return nil
}

func (c *Consumer) handleDeliveryError(ctx context.Context, job EmailJob, sendErr error) error {
	var deliveryErr *sender.DeliveryError
	if !errors.As(sendErr, &deliveryErr) || deliveryErr.Classification.Type == sender.BounceTypeNone {
		if markErr := c.Store.MarkFailed(ctx, job.RecordID, sendErr.Error()); markErr != nil {
			return fmt.Errorf("send email: %w; mark failed: %v", sendErr, markErr)
		}
		return sendErr
	}

	smtpCode := deliveryErr.Classification.SMTPCode
	switch deliveryErr.Classification.Type {
	case sender.BounceTypeHard:
		if err := c.Store.MarkBounced(ctx, job.RecordID, sendErr.Error(), string(sender.BounceTypeHard), smtpCode, job.RetryCount); err != nil {
			return err
		}
		c.trackVerificationEmailBounced(ctx, job.RecordID, string(sender.BounceTypeHard))
		if err := c.Store.Blacklist(ctx, job.To, sendErr.Error()); err != nil {
			return err
		}
		return sendErr
	case sender.BounceTypeSoft:
		if err := c.Store.MarkBounced(ctx, job.RecordID, sendErr.Error(), string(sender.BounceTypeSoft), smtpCode, job.RetryCount); err != nil {
			return err
		}
		c.trackVerificationEmailBounced(ctx, job.RecordID, string(sender.BounceTypeSoft))
		if job.RetryCount >= len(softBounceRetryIntervals) {
			if err := c.Store.MarkFailed(ctx, job.RecordID, ErrSoftBounceExceeded.Error()); err != nil {
				return err
			}
			return ErrSoftBounceExceeded
		}
		delay := softBounceRetryIntervals[job.RetryCount]
		retryJob := job
		retryJob.RetryCount++
		c.Scheduler.AfterFunc(delay, func() {
			if err := c.Queue.EnqueueContext(context.Background(), c.QueueName, retryJob); err != nil {
				log.Printf("email-worker: failed to enqueue soft-bounce retry record_id=%q: %v", job.RecordID, err)
			}
		})
		return nil
	default:
		if markErr := c.Store.MarkFailed(ctx, job.RecordID, sendErr.Error()); markErr != nil {
			return fmt.Errorf("send email: %w; mark failed: %v", sendErr, markErr)
		}
		return sendErr
	}
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
	}{OTP: job.OTP, MagicLink: job.MagicLink, ExpiresIn: job.ExpiresIn}

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

func (c *Consumer) trackVerificationEmailSent(ctx context.Context, recordID string) {
	c.trackRecordEvent(ctx, recordID, "verification_email_sent", nil)
}

func (c *Consumer) trackVerificationEmailBounced(ctx context.Context, recordID, bounceType string) {
	c.trackRecordEvent(ctx, recordID, "verification_email_bounced", map[string]any{"bounce_type": bounceType})
}

func (c *Consumer) trackRecordEvent(ctx context.Context, recordID, eventName string, props map[string]any) {
	if c.Analytics == nil {
		return
	}
	record, err := c.Store.LookupAnalyticsRecordByID(ctx, recordID)
	if err != nil {
		log.Printf("email-worker analytics: lookup record_id=%q failed: %v", recordID, err)
		return
	}
	if strings.TrimSpace(record.UserID) == "" {
		log.Printf("email-worker analytics: skip event=%q record_id=%q missing user_id", eventName, recordID)
		return
	}
	if strings.TrimSpace(record.Email) == "" {
		log.Printf("email-worker analytics: skip event=%q record_id=%q missing email", eventName, recordID)
		return
	}
	if err := c.Analytics.Track(ctx, analytics.Event{
		Name:       eventName,
		UserID:     record.UserID,
		Email:      record.Email,
		Timestamp:  time.Now().UTC(),
		Properties: props,
	}); err != nil {
		log.Printf("email-worker analytics: track event=%q record_id=%q failed: %v", eventName, recordID, err)
	}
}
