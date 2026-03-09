package monitoring

import (
	"context"
	"log"
	"time"
)

type QueueLengthReader interface {
	QueueLengthContext(ctx context.Context, queueName string) (int64, error)
}

type QueueBacklogCollector struct {
	Queue        QueueLengthReader
	QueueName    string
	PollInterval time.Duration
	Metrics      *Metrics
	Logger       *log.Logger
}

func (c *QueueBacklogCollector) Run(ctx context.Context) error {
	if c == nil || c.Queue == nil || c.Metrics == nil {
		return nil
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 15 * time.Second
	}

	c.poll(ctx)

	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

func (c *QueueBacklogCollector) poll(ctx context.Context) {
	backlog, err := c.Queue.QueueLengthContext(ctx, c.QueueName)
	if err != nil {
		c.Metrics.IncQueueBacklogPollFailure(c.QueueName)
		if c.Logger != nil {
			c.Logger.Printf("email-worker metrics: failed to poll queue backlog for %q: %v", c.QueueName, err)
		}
		return
	}
	c.Metrics.SetQueueBacklog(c.QueueName, backlog)
}
