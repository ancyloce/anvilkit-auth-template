package config

import (
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"anvilkit-auth-template/modules/common-go/pkg/analytics"
	"anvilkit-auth-template/modules/common-go/pkg/email"
)

const (
	defaultDBDSN           = "postgres://postgres:postgres@localhost:5432/auth?sslmode=disable"
	defaultRedisAddr       = "localhost:6379"
	defaultQueueName       = "email:send"
	defaultQueueTimeoutSec = 5
	defaultQueuePollSec    = 15
	defaultWebhookAddr     = ":8082"
	defaultMetricsAddr     = ":9090"
	defaultSMTPHost        = "localhost"
	defaultSMTPPort        = 1025
	defaultSMTPFromEmail   = "noreply@example.com"
	defaultSMTPFromName    = "Anvilkit Auth"
)

type Config struct {
	DBDSN             string
	RedisAddr         string
	QueueName         string
	QueuePopTimeout   time.Duration
	QueuePollInterval time.Duration
	WebhookAddr       string
	MetricsAddr       string
	WebhookSecret     string
	SMTPHost          string
	SMTPPort          int
	SMTPUsername      string
	SMTPPassword      string
	SMTPFromEmail     string
	SMTPFromName      string
	Analytics         analytics.Config
}

func LoadFromEnv() (Config, error) {
	queueTimeoutSec, err := getPositiveIntFromEnv("EMAIL_QUEUE_POP_TIMEOUT_SEC", defaultQueueTimeoutSec)
	if err != nil {
		return Config{}, err
	}
	queuePollSec, err := getPositiveIntFromEnv("EMAIL_QUEUE_BACKLOG_POLL_SEC", defaultQueuePollSec)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		DBDSN:             getStringFromEnv("DB_DSN", defaultDBDSN),
		RedisAddr:         getStringFromEnv("REDIS_ADDR", defaultRedisAddr),
		QueueName:         getStringFromEnv("EMAIL_QUEUE_NAME", defaultQueueName),
		QueuePopTimeout:   time.Duration(queueTimeoutSec) * time.Second,
		QueuePollInterval: time.Duration(queuePollSec) * time.Second,
		WebhookAddr:       getStringFromEnv("EMAIL_WEBHOOK_ADDR", defaultWebhookAddr),
		MetricsAddr:       getStringFromEnv("EMAIL_METRICS_ADDR", defaultMetricsAddr),
		WebhookSecret:     strings.TrimSpace(os.Getenv("EMAIL_WEBHOOK_SECRET")),
		SMTPHost:          getStringFromEnv("SMTP_HOST", defaultSMTPHost),
		SMTPPort:          getIntFromEnv("SMTP_PORT", defaultSMTPPort),
		SMTPUsername:      getStringFromEnv("SMTP_USERNAME", ""),
		SMTPPassword:      os.Getenv("SMTP_PASSWORD"),
		SMTPFromEmail:     getStringFromEnv("SMTP_FROM_EMAIL", defaultSMTPFromEmail),
		SMTPFromName:      getStringFromEnv("SMTP_FROM_NAME", defaultSMTPFromName),
	}
	cfg.Analytics, err = analytics.LoadConfigFromEnv()
	if err != nil {
		return Config{}, err
	}

	if strings.TrimSpace(cfg.DBDSN) == "" {
		return Config{}, fmt.Errorf("DB_DSN cannot be empty")
	}
	if strings.TrimSpace(cfg.RedisAddr) == "" {
		return Config{}, fmt.Errorf("REDIS_ADDR cannot be empty")
	}
	if strings.TrimSpace(cfg.QueueName) == "" {
		return Config{}, fmt.Errorf("EMAIL_QUEUE_NAME cannot be empty")
	}
	if strings.TrimSpace(cfg.WebhookAddr) == "" {
		return Config{}, fmt.Errorf("EMAIL_WEBHOOK_ADDR cannot be empty")
	}
	if strings.TrimSpace(cfg.MetricsAddr) == "" {
		return Config{}, fmt.Errorf("EMAIL_METRICS_ADDR cannot be empty")
	}
	if cfg.WebhookSecret == "" {
		return Config{}, fmt.Errorf("EMAIL_WEBHOOK_SECRET cannot be empty")
	}
	if strings.TrimSpace(cfg.SMTPHost) == "" {
		return Config{}, fmt.Errorf("SMTP_HOST cannot be empty")
	}
	if cfg.SMTPPort <= 0 || cfg.SMTPPort > 65535 {
		return Config{}, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}
	if _, err := mail.ParseAddress(cfg.SMTPFromEmail); err != nil {
		return Config{}, fmt.Errorf("SMTP_FROM_EMAIL must be a valid email address")
	}

	return cfg, nil
}

func (c Config) SMTPConfig() email.SMTPConfig {
	return email.SMTPConfig{
		Host:      c.SMTPHost,
		Port:      c.SMTPPort,
		Username:  c.SMTPUsername,
		Password:  c.SMTPPassword,
		FromEmail: c.SMTPFromEmail,
		FromName:  c.SMTPFromName,
	}
}

func getStringFromEnv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func getIntFromEnv(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return value
}

func getPositiveIntFromEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer", key)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", key)
	}
	return value, nil
}
