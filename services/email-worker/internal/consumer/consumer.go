package consumer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"anvilkit-auth-template/services/email-worker/internal/sender"
)

var (
	ErrNilQueue     = errors.New("nil_queue")
	ErrNilSender    = errors.New("nil_sender")
	ErrNilStore     = errors.New("nil_store")
	ErrEmptyQueue   = errors.New("empty_queue_name")
	ErrInvalidJob   = errors.New("invalid_email_job")
	ErrEmptyRecord  = errors.New("empty_record_id")
	ErrEmptyToEmail = errors.New("empty_to_email")
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
	RecordID string `json:"record_id"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	HTMLBody string `json:"html_body"`
	TextBody string `json:"text_body"`
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
		return fmt.Errorf("%w: %v", ErrInvalidJob, ErrEmptyToEmail)
	}

	externalID, err := c.Sender.Send(ctx, sender.Request{
		To:       job.To,
		Subject:  job.Subject,
		HTMLBody: job.HTMLBody,
		TextBody: job.TextBody,
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
