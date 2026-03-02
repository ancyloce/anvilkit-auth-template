package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var (
	ErrNilRedisClient    = errors.New("nil_redis_client")
	ErrEmptyQueueName    = errors.New("empty_queue_name")
	ErrNilDestination    = errors.New("nil_destination")
	ErrInvalidBLPopReply = errors.New("invalid_blpop_reply")
)

// RedisClient captures the subset of Redis commands used by the queue abstraction.
type RedisClient interface {
	RPush(ctx context.Context, key string, values ...interface{}) *goredis.IntCmd
	BLPop(ctx context.Context, timeout time.Duration, keys ...string) *goredis.StringSliceCmd
	LLen(ctx context.Context, key string) *goredis.IntCmd
}

// RedisQueue provides JSON payload queue operations backed by Redis lists.
type RedisQueue struct {
	client RedisClient
}

func New(client RedisClient) (*RedisQueue, error) {
	if client == nil {
		return nil, ErrNilRedisClient
	}
	return &RedisQueue{client: client}, nil
}

// Enqueue appends a JSON-serialized payload into queueName using RPUSH.
func (q *RedisQueue) Enqueue(queueName string, payload any) error {
	return q.EnqueueContext(context.Background(), queueName, payload)
}

func (q *RedisQueue) EnqueueContext(ctx context.Context, queueName string, payload any) error {
	if err := validateQueueName(queueName); err != nil {
		return err
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return q.client.RPush(ctx, queueName, string(encoded)).Err()
}

// Dequeue blocks on queueName (BLPOP) for up to timeout and returns the raw JSON payload.
// The bool result reports whether a payload was received before timeout.
func (q *RedisQueue) Dequeue(queueName string, timeout time.Duration) (json.RawMessage, bool, error) {
	return q.DequeueContext(context.Background(), queueName, timeout)
}

func (q *RedisQueue) DequeueContext(ctx context.Context, queueName string, timeout time.Duration) (json.RawMessage, bool, error) {
	if err := validateQueueName(queueName); err != nil {
		return nil, false, err
	}

	res, err := q.client.BLPop(ctx, timeout, queueName).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(res) != 2 {
		return nil, false, ErrInvalidBLPopReply
	}

	return json.RawMessage(res[1]), true, nil
}

// DequeueInto blocks on queueName (BLPOP), then JSON-deserializes into out.
// The bool result reports whether a payload was received before timeout.
func (q *RedisQueue) DequeueInto(queueName string, timeout time.Duration, out any) (bool, error) {
	return q.DequeueIntoContext(context.Background(), queueName, timeout, out)
}

func (q *RedisQueue) DequeueIntoContext(ctx context.Context, queueName string, timeout time.Duration, out any) (bool, error) {
	if out == nil {
		return false, ErrNilDestination
	}

	payload, ok, err := q.DequeueContext(ctx, queueName, timeout)
	if err != nil || !ok {
		return ok, err
	}

	if err := json.Unmarshal(payload, out); err != nil {
		return false, err
	}
	return true, nil
}

// QueueLength returns LLEN(queueName).
func (q *RedisQueue) QueueLength(queueName string) (int64, error) {
	return q.QueueLengthContext(context.Background(), queueName)
}

func (q *RedisQueue) QueueLengthContext(ctx context.Context, queueName string) (int64, error) {
	if err := validateQueueName(queueName); err != nil {
		return 0, err
	}
	return q.client.LLen(ctx, queueName).Result()
}

func validateQueueName(queueName string) error {
	if strings.TrimSpace(queueName) == "" {
		return ErrEmptyQueueName
	}
	return nil
}
